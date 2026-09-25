package store

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// reserveRuntimePlacement shares the deployment lock with restore and node removal.
// A Session creation retry never reaches this function. A node enrolled with
// another Core address receives no new sandbox; restores still reach it.
func reserveRuntimePlacement(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, publicURL string) error {
	d, err := q.LockRuntimeDeployment(ctx)
	if err != nil {
		return err
	}
	if d.Mode == "direct" {
		if d.Maintenance {
			return ErrRuntimeNodeUnavailable
		}
		// E2B guests reach Core over the internet. A selection saved before the
		// public URL became loopback admits nothing, while its existing sandboxes
		// stay reachable for cleanup through the loaded provider.
		if LoopbackOrigin(publicURL) {
			return ErrSandboxPublicURLUnreachable
		}
		return nil
	}
	if d.ProviderKind == "" {
		if d.WebManaged {
			return ErrRuntimeNodeUnavailable
		}
		return nil
	}
	if d.Maintenance {
		return ErrRuntimeNodeUnavailable
	}
	rows, err := q.ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return err
	}
	var chosen *sqlc.ListRuntimeNodesRow
	for i := range rows {
		n := &rows[i]
		if !n.Online || !n.ProviderReady || n.Active >= int64(n.MaxActive) || n.Retained >= int64(n.MaxRetained) || (d.WebManaged && n.CoreUrl != publicURL) {
			continue
		}
		if chosen == nil || n.Active < chosen.Active {
			chosen = n
		}
	}
	if chosen == nil {
		return ErrRuntimeNodeUnavailable
	}
	return q.CreateSessionRuntimePlacement(ctx, sqlc.CreateSessionRuntimePlacementParams{SessionID: session, NodeID: chosen.ID})
}

// ResolveRuntimeNode includes deleted Sessions so owned cleanup remains routable.
func (s *Store) ResolveRuntimeNode(ctx context.Context, tenant, environment string) (string, error) {
	allocation, err := s.GetRuntimeAllocation(ctx, tenant, environment)
	if err != nil {
		return "", err
	}
	if allocation.NodeID == "" {
		return "", ErrRuntimeNodeUnavailable
	}
	return allocation.NodeID, nil
}
func reserveRuntimeRestore(ctx context.Context, q *sqlc.Queries, node pgtype.UUID) error {
	if !node.Valid {
		return nil
	}
	if _, err := q.LockRuntimeDeployment(ctx); err != nil {
		return err
	}
	nodes, err := q.ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.ID == node {
			if !n.Online || !n.ProviderReady || n.Active >= int64(n.MaxActive) {
				return ErrRuntimeNodeUnavailable
			}
			return nil
		}
	}
	return ErrRuntimeNodeUnavailable
}
