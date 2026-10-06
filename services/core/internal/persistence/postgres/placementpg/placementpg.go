// Package placementpg loads the placement facts of the sandbox deployment and
// applies the placements package placement decides, on the caller's
// transaction-bound queries. It has no Store, pool or transaction runner:
// deploymentpg, sessionpg and Session creation call it inside their own
// transactions, which supply the authority.
package placementpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

// LockDeployment locks the deployment and returns it as placement reads it.
// Admission, placement, restore and node removal serialize on this lock.
func LockDeployment(ctx context.Context, q *sqlc.Queries) (placement.Deployment, error) {
	d, err := q.LockRuntimeDeployment(ctx)
	if err != nil {
		return placement.Deployment{}, err
	}
	return placement.Deployment{
		InstallationID: uuidString(d.InstallationID), Provider: d.ProviderKind, Mode: d.Mode,
		Generation: uint64(d.Generation), WebManaged: d.WebManaged, AdmissionPaused: d.AdmissionPaused,
		Resetting: d.ResetClear.Valid, Specification: d.Specification,
	}, nil
}

// LoadNodes returns the installation's nodes with their presence and usage.
func LoadNodes(ctx context.Context, q *sqlc.Queries) ([]placement.Node, error) {
	rows, err := q.ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	nodes := make([]placement.Node, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, node(row))
	}
	return nodes, nil
}

// ReservePlacement reserves the placement for the Session's Environment.
func ReservePlacement(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, p placement.Placement) error {
	id, err := pgunit.ParseID(p.NodeID)
	if err != nil {
		return err
	}
	return q.CreateSessionRuntimePlacement(ctx, sqlc.CreateSessionRuntimePlacementParams{SessionID: session, NodeID: id, Generation: int64(p.Generation)})
}

// LoadReserved returns the node placement reserved for the Environment.
func LoadReserved(ctx context.Context, q *sqlc.Queries, environment pgtype.UUID) (placement.Reserved, error) {
	row, err := q.GetRuntimePlacement(ctx, environment)
	if err != nil {
		return placement.Reserved{}, err
	}
	return placement.Reserved{NodeID: uuidString(row.NodeID), Generation: uint64(row.DeploymentGeneration.Int64), Released: row.ReleasedAt.Valid, Available: row.Available}, nil
}

// ReleasePlacement releases the node placement of the Environment, if any.
func ReleasePlacement(ctx context.Context, q *sqlc.Queries, environment pgtype.UUID) error {
	return q.ReleaseRuntimePlacement(ctx, environment)
}

// LoadRestore locks the deployment and returns what restoring suspended
// compute of the generation on the node reads.
func LoadRestore(ctx context.Context, q *sqlc.Queries, nodeID pgtype.UUID, generation uint64) (placement.Restore, error) {
	if _, err := q.LockRuntimeDeployment(ctx); err != nil {
		return placement.Restore{}, err
	}
	restore := placement.Restore{Generation: generation}
	rows, err := q.ListRuntimeNodes(ctx, nodeID)
	if err != nil || len(rows) == 0 {
		return restore, err
	}
	n := node(rows[0])
	restore.Node = &n
	restore.GenerationReady, err = q.NodeGenerationReady(ctx, sqlc.NodeGenerationReadyParams{NodeID: nodeID, Generation: int64(generation)})
	return restore, err
}

// ComputeBlocksAdmission reports whether the Session's managed compute is in
// its suspension cycle: quiescing, suspending, suspended, restoring or waking.
func ComputeBlocksAdmission(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (bool, error) {
	return q.RuntimeComputeBlocksAdmission(ctx, session)
}

func node(row sqlc.ListRuntimeNodesRow) placement.Node {
	n := placement.Node{
		ID: uuidString(row.ID), Online: row.Online, ServingReady: row.ServingReady, TargetState: row.TargetState,
		Active: row.Active, Retained: row.Retained, MaxActive: int(row.MaxActive), MaxRetained: int(row.MaxRetained), CoreURL: row.CoreUrl,
	}
	if row.ReadyGeneration.Valid {
		ready := uint64(row.ReadyGeneration.Int64)
		n.ReadyGeneration = &ready
	}
	return n
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
