package deploymentpg

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/placementpg"
)

func (t *allocationTx) LoadGenerationSpecification(generation uint64) (deployment.GenerationSpecification, error) {
	return (unit{ctx: t.ctx, q: t.q}).LoadGenerationSpecification(generation)
}

func (t *allocationTx) LoadSuspensionDemand(current deployment.Allocation) (deployment.SuspensionDemand, error) {
	var result deployment.SuspensionDemand
	var err error
	result.Deployment, err = placementpg.LockDeployment(t.ctx, t.q)
	if err != nil {
		return result, err
	}
	inFlight, err := t.q.ListRuntimeSuspensionInFlightNodes(t.ctx)
	if err != nil || result.Deployment.Resetting {
		return result, err
	}
	for _, node := range inFlight {
		result.InFlightNodes = append(result.InFlightNodes, uuidString(node))
	}
	result.Nodes, err = placementpg.LoadNodes(t.ctx, t.q)
	if err != nil {
		return result, err
	}
	node, err := parseID(current.NodeID)
	if err != nil {
		return result, err
	}
	generations, err := t.q.ListWaitingRuntimeRestoreGenerations(t.ctx, node)
	if err != nil {
		return result, err
	}
	for _, generation := range generations {
		ready, err := t.q.NodeGenerationReady(t.ctx, sqlc.NodeGenerationReadyParams{NodeID: node, Generation: generation.Int64})
		if err != nil {
			return result, err
		}
		if ready {
			result.RestoreWaiting = true
			return result, nil
		}
	}
	return result, nil
}

func (t *allocationTx) PlacementDemand(after deployment.PlacementDemandCursor) ([]deployment.PlacementDemand, deployment.PlacementDemandCursor, error) {
	rows, next, err := loadPlacementDemand(t.ctx, t.q, after)
	if err != nil {
		return nil, deployment.PlacementDemandCursor{}, err
	}
	qualified := rows[:0]
	for _, row := range rows {
		if row.Retained {
			tenant, environment, err := allocationKey(deployment.AllocationKey{TenantID: row.TenantID, EnvironmentID: row.ID})
			if err != nil {
				return nil, next, err
			}
			owner, found, err := findAllocation(t.ctx, t.q, tenant, environment)
			if err != nil {
				return nil, next, err
			}
			if !found {
				continue
			}
			id, err := parseID(owner.ID)
			if err != nil {
				return nil, next, err
			}
			replaceable, err := t.q.CanReplaceRuntimeAllocation(t.ctx, id)
			if err != nil {
				return nil, next, err
			}
			if !replaceable {
				continue
			}
			retained, err := t.q.CanRetainRuntimeEnvironment(t.ctx, id)
			if err != nil {
				return nil, next, err
			}
			if !retained {
				continue
			}
		}
		qualified = append(qualified, row)
	}
	return qualified, next, nil
}

func loadPlacementDemand(ctx context.Context, q *sqlc.Queries, after deployment.PlacementDemandCursor) ([]deployment.PlacementDemand, deployment.PlacementDemandCursor, error) {
	var next deployment.PlacementDemandCursor
	id, err := cursor(after.EnvironmentID)
	if err != nil {
		return nil, next, err
	}
	rows, err := q.ListPlacementDemand(ctx, sqlc.ListPlacementDemandParams{AfterID: id, AfterTime: pgtype.Timestamptz{Time: after.At, Valid: true}, UntilTime: pgtype.Timestamptz{Time: after.Until, Valid: !after.Until.IsZero()}})
	if err != nil {
		return nil, next, err
	}
	result := make([]deployment.PlacementDemand, 0, len(rows))
	for _, row := range rows {
		result = append(result, deployment.PlacementDemand{UnallocatedEnvironment: deployment.UnallocatedEnvironment{ID: uuidString(row.ID), TenantID: uuidString(row.TenantID)}, Engine: row.Engine, Retained: row.Retained, At: row.DemandedAt.Time})
	}
	// Even a short final page carries its database horizon, so a caller
	// yielding partway through can resume without admitting new arrivals.
	if len(rows) > 0 {
		next.Until = rows[0].ScanUntil.Time
	}
	if len(rows) == 32 {
		last := rows[len(rows)-1]
		next = deployment.PlacementDemandCursor{At: last.DemandedAt.Time, EnvironmentID: uuidString(last.ID), Until: last.ScanUntil.Time}
	}
	return result, next, nil
}
