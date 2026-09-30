package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ListRuntimeLifecycleNodes includes offline nodes: loss of connectivity never
// releases their resources. The empty identity is the single legacy lifecycle.
func (s *Store) ListRuntimeLifecycleNodes(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	if err := s.checkExecutionOwnership(ctx); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListRuntimeLifecycleNodes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(rows))
	for _, id := range rows {
		result = append(result, runtimeUUID(id))
	}
	return result, nil
}

func runtimeNodePage(node, after string) (pgtype.UUID, pgtype.UUID, error) {
	var nodeID pgtype.UUID
	afterID := pgtype.UUID{Valid: true}
	var err error
	if node != "" {
		nodeID, err = parseConnectionGeneration(node)
		if err != nil {
			return nodeID, afterID, err
		}
	}
	if after != "" {
		afterID, err = parseID(after)
	}
	return nodeID, afterID, err
}

// ListRuntimeAllocationsForNode advances independently of every other node.
func (s *Store) ListRuntimeAllocationsForNode(ctx context.Context, node, after string) ([]RuntimeAllocation, error) {
	nodeID, afterID, err := runtimeNodePage(node, after)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	if err := s.checkExecutionOwnership(ctx); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListRuntimeAllocationsForNode(ctx, sqlc.ListRuntimeAllocationsForNodeParams{NodeID: nodeID, AfterID: afterID})
	if err != nil {
		return nil, err
	}
	result := make([]RuntimeAllocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, runtimeAllocationFromRow(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, row.Expired))
	}
	return result, nil
}

// ListUnallocatedHostedEnvironmentsForNode reads the committed placement; it
// never selects a replacement node for an unavailable original placement.
func (s *Store) ListUnallocatedHostedEnvironmentsForNode(ctx context.Context, node, after string) ([]UnallocatedHostedEnvironment, error) {
	nodeID, afterID, err := runtimeNodePage(node, after)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	if err := s.checkExecutionOwnership(ctx); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListUnallocatedHostedEnvironmentsForNode(ctx, sqlc.ListUnallocatedHostedEnvironmentsForNodeParams{NodeID: nodeID, AfterID: afterID})
	if err != nil {
		return nil, err
	}
	result := make([]UnallocatedHostedEnvironment, 0, len(rows))
	for _, row := range rows {
		result = append(result, UnallocatedHostedEnvironment{ID: runtimeUUID(row.ID), TenantID: runtimeUUID(row.TenantID)})
	}
	return result, nil
}

// ResolveRuntimeLifecycleNode routes direct provisioning before an allocation
// exists. An existing allocation must agree with its immutable placement.
func (s *Store) ResolveRuntimeLifecycleNode(ctx context.Context, tenant, environment string) (string, error) {
	lookup, err := deviceLookup(tenant, environment)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	if err := s.checkExecutionOwnership(ctx); err != nil {
		return "", err
	}
	row, err := s.queries.GetRuntimeLifecyclePlacement(ctx, sqlc.GetRuntimeLifecyclePlacementParams{TenantID: lookup.TenantID, ID: lookup.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if row.ProviderKind == "" || row.Mode == "direct" {
		if row.PlacementNodeID.Valid || row.AllocationNodeID.Valid {
			return "", deployment.ErrNodeUnavailable
		}
		return "", nil
	}
	if !row.PlacementNodeID.Valid || (row.AllocationID.Valid && row.AllocationNodeID != row.PlacementNodeID) || (!row.AllocationID.Valid && row.ReleasedAt.Valid) {
		return "", deployment.ErrNodeUnavailable
	}
	return runtimeUUID(row.PlacementNodeID), nil
}
