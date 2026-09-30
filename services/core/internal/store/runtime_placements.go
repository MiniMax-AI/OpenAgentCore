package store

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
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
	if d.ResetClear.Valid {
		return ErrSandboxResetAdmission
	}
	if d.Mode == "direct" {
		if d.AdmissionPaused {
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
	if d.AdmissionPaused {
		return ErrRuntimeNodeUnavailable
	}
	rows, err := q.ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return err
	}
	var chosen *sqlc.ListRuntimeNodesRow
	preparing := false
	for i := range rows {
		n := &rows[i]
		if n.Online && n.TargetState == "preparing" && n.Active < int64(n.MaxActive) && n.Retained < int64(n.MaxRetained) && (!d.WebManaged || n.CoreUrl == publicURL) {
			preparing = true
		}
		if !n.Online || !n.ServingReady || !n.ReadyGeneration.Valid || n.Active >= int64(n.MaxActive) || n.Retained >= int64(n.MaxRetained) || (d.WebManaged && n.CoreUrl != publicURL) {
			continue
		}
		if chosen == nil || n.ReadyGeneration.Int64 > chosen.ReadyGeneration.Int64 || n.ReadyGeneration.Int64 == chosen.ReadyGeneration.Int64 && n.Active < chosen.Active {
			chosen = n
		}
	}
	if chosen == nil {
		if preparing {
			return ErrSandboxNodesPreparing
		}
		return ErrRuntimeNodeUnavailable
	}
	return q.CreateSessionRuntimePlacement(ctx, sqlc.CreateSessionRuntimePlacementParams{SessionID: session, NodeID: chosen.ID, Generation: chosen.ReadyGeneration.Int64})
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
func reserveRuntimeRestore(ctx context.Context, q *sqlc.Queries, node pgtype.UUID, generation pgtype.Int8) error {
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
			ready, err := q.NodeGenerationReady(ctx, sqlc.NodeGenerationReadyParams{NodeID: node, Generation: generation.Int64})
			if err != nil {
				return err
			}
			if !generation.Valid || !n.Online || !ready || n.Active >= int64(n.MaxActive) {
				return ErrRuntimeNodeUnavailable
			}
			return nil
		}
	}
	return ErrRuntimeNodeUnavailable
}

// ResolveRuntimeGeneration includes deleted Sessions and never substitutes the target.
func (s *Store) ResolveRuntimeGeneration(ctx context.Context, ref sandbox.Reference) (string, uint64, error) {
	a, err := s.GetRuntimeAllocation(ctx, ref.TenantID, ref.EnvironmentID)
	if err != nil {
		return "", 0, err
	}
	if a.State == "released" || a.ID != ref.AllocationID || a.NodeID == "" || a.DeploymentGeneration == 0 {
		return "", 0, ErrRuntimeNodeUnavailable
	}
	return a.NodeID, a.DeploymentGeneration, nil
}
