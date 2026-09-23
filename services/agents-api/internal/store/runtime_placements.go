package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// reserveRuntimePlacement shares the deployment lock with restore and node removal.
// A Session creation retry never reaches this function.
func reserveRuntimePlacement(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, selected string) error {
	d, err := q.LockRuntimeDeployment(ctx)
	if err != nil {
		return err
	}
	if d.ProviderKind == "" {
		if d.WebManaged || selected != "" {
			return ErrRuntimeNodeUnavailable
		}
		return nil
	}
	if d.Maintenance {
		return ErrRuntimeNodeUnavailable
	}
	rows, err := q.ListRuntimeNodes(ctx)
	if err != nil {
		return err
	}
	var chosen *sqlc.ListRuntimeNodesRow
	for i := range rows {
		n := &rows[i]
		if selected != "" && runtimeUUID(n.ID) != selected {
			continue
		}
		if !n.Online || !n.ProviderReady || n.Active >= int64(n.MaxActive) || n.Retained >= int64(n.MaxRetained) {
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

func (s *Store) GetSessionRuntimePlacement(ctx context.Context, tenant, session string) (RuntimePlacement, error) {
	value, err := s.GetSession(ctx, tenant, session)
	if err != nil {
		return RuntimePlacement{}, err
	}
	if value.Environment == nil {
		return RuntimePlacement{}, ErrNotFound
	}
	id, err := parseID(value.Environment.ID)
	if err != nil {
		return RuntimePlacement{}, err
	}
	p, err := s.queries.GetRuntimePlacement(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimePlacement{}, ErrNotFound
	}
	if err != nil {
		return RuntimePlacement{}, err
	}
	return RuntimePlacement{Diagnostic: p.ObservationError, NodeID: runtimeUUID(p.NodeID), NodeName: p.Name, Available: p.Available && !p.ReleasedAt.Valid, State: p.State, ComputePhase: p.ComputePhase}, nil
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
	nodes, err := q.ListRuntimeNodes(ctx)
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
