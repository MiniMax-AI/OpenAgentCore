package store

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// RecordRuntimeObservation changes diagnostics only for the observed lifecycle revision.
// It never releases ownership, changes public readiness or authorizes replacement.
func (s *Store) RecordRuntimeObservation(ctx context.Context, owner RuntimeAllocation, diagnostic string) error {
	switch diagnostic {
	case "", "node_unavailable", "resource_missing", "compute_unconfirmed", "ownership_mismatch", "provider_unavailable":
	default:
		return ErrInvalidInput
	}
	if owner.NodeID == "" {
		return nil
	}
	_, err := s.mutateRuntimeAllocation(ctx, owner, false, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		if row.ComputeRevision != owner.ComputeRevision || row.State != owner.State || row.State == "released" {
			return row, nil
		}
		err := q.SetRuntimeObservation(ctx, sqlc.SetRuntimeObservationParams{ID: row.ID, ComputeRevision: owner.ComputeRevision, State: owner.State, ObservationError: diagnostic})
		return row, err
	})
	return err
}
func (s *Store) RuntimeNodeAvailable(ctx context.Context, node string) (bool, error) {
	id, err := parseConnectionGeneration(node)
	if err != nil {
		return false, err
	}
	return runtimeNodeAvailable(ctx, s.queries, id)
}
func runtimeNodeAvailable(ctx context.Context, q *sqlc.Queries, node pgtype.UUID) (bool, error) {
	nodes, err := q.ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return false, err
	}
	for _, n := range nodes {
		if n.ID == node {
			return n.Online, nil
		}
	}
	return false, nil
}
