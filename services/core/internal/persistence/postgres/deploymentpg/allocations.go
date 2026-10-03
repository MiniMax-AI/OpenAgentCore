package deploymentpg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/placementpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// allocation converts a stored allocation with the facts of its Session.
func allocation(row sqlc.RuntimeAllocation, session, tenant pgtype.UUID, deleted pgtype.Timestamptz, expired bool) deployment.Allocation {
	return deployment.Allocation{
		DeploymentGeneration: uint64(row.DeploymentGeneration.Int64), NodeID: uuidString(row.NodeID), ObservationError: row.ObservationError,
		ComputePhase: row.ComputePhase, ComputeRevision: row.ComputeRevision, ComputeState: row.ComputeState,
		ComputeActivityAt: row.ComputeActivityAt.Time, ComputeWakeRequested: row.ComputeWakeRequested, ComputeRetainedUntil: timestamp(row.ComputeRetainedUntil),
		ID: uuidString(row.ID), EnvironmentID: uuidString(row.EnvironmentID), SessionID: uuidString(session), TenantID: uuidString(tenant),
		DeviceID: uuidString(row.DeviceID), ProviderKey: uuidString(row.ProviderKey), State: row.State, CreateSettled: row.CreateSettled,
		SessionDeleted: deleted.Valid, Expired: expired, CreatedAt: row.CreatedAt.Time, KeptAt: row.KeptAt.Time,
	}
}

// allocationKey returns the stored form of the key's identifiers.
func allocationKey(key deployment.AllocationKey) (pgtype.UUID, pgtype.UUID, error) {
	tenant, err := parseID(key.TenantID)
	if err != nil {
		return tenant, pgtype.UUID{}, err
	}
	environment, err := parseID(key.EnvironmentID)
	return tenant, environment, err
}

// cursor returns the stored form of a page cursor: an empty one starts at
// the beginning.
func cursor(after string) (pgtype.UUID, error) {
	if after == "" {
		return pgtype.UUID{Valid: true}, nil
	}
	return parseID(after)
}

// lifecycleNode returns the stored form of a lifecycle's node: an empty one
// names the lifecycle without a node.
func lifecycleNode(nodeID string) (pgtype.UUID, error) {
	if nodeID == "" {
		return pgtype.UUID{}, nil
	}
	return parseID(nodeID)
}

func (e *Execution) WithReservation(ctx context.Context, key deployment.AllocationKey, apply func(sessions.LockedSession, deployment.ReservationTx) error) error {
	tenant, environment, err := allocationKey(key)
	if err != nil {
		return err
	}
	return translate(e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		owned, err := q.GetEnvironment(ctx, sqlc.GetEnvironmentParams{TenantID: tenant, ID: environment})
		if errors.Is(err, pgx.ErrNoRows) {
			// sessions.ErrNotFound keeps the public 404 that writeStoreError maps.
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		session := owned.Environment.SessionID
		locked, err := sessionpg.LockSession(ctx, q, tenant, session)
		if err != nil {
			return err
		}
		t := &reservationTx{SessionTx: sessionpg.BindSession(q, tenant, session), ctx: ctx, q: q, tenant: tenant, environment: environment, session: session}
		if err := apply(locked, t); err != nil {
			return err
		}
		return sessionpg.PruneChanges(ctx, q, session)
	}))
}

func (e *Execution) WithAllocation(ctx context.Context, key deployment.AllocationKey, apply func(deployment.AllocationTx) error) error {
	return e.withAllocation(ctx, key, func(t *allocationTx) error { return apply(t) })
}

func (e *Execution) WithAllocationCleanup(ctx context.Context, key deployment.AllocationKey, apply func(deployment.AllocationCleanupTx) error) error {
	return e.withAllocation(ctx, key, func(t *allocationTx) error {
		return apply(&cleanupTx{allocationTx: t, SessionTx: sessionpg.BindSession(t.q, t.tenant, t.session)})
	})
}

// withAllocation locks the Session that owns the Environment's allocation,
// deleted or not, runs apply and prunes the Session's journal.
func (e *Execution) withAllocation(ctx context.Context, key deployment.AllocationKey, apply func(*allocationTx) error) error {
	tenant, environment, err := allocationKey(key)
	if err != nil {
		return err
	}
	return translate(e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: tenant, EnvironmentID: environment})
		if errors.Is(err, pgx.ErrNoRows) {
			return deployment.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := sessionpg.LockSession(ctx, q, row.TenantID, row.SessionID); err != nil {
			return err
		}
		if err := apply(&allocationTx{ctx: ctx, q: q, tenant: tenant, environment: environment, session: row.SessionID}); err != nil {
			return err
		}
		return sessionpg.PruneChanges(ctx, q, row.SessionID)
	}))
}

func (e *Execution) ClearWake(ctx context.Context, allocationID string, observed time.Time) error {
	id, err := parseID(allocationID)
	if err != nil {
		return err
	}
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return sqlc.New(tx).ClearRuntimeWake(ctx, sqlc.ClearRuntimeWakeParams{ID: id, ComputeActivityAt: pgtype.Timestamptz{Time: observed, Valid: true}})
	})
}

func (s *Store) WithActivity(ctx context.Context, key deployment.AllocationKey, apply func(sessions.LockedSession, deployment.ActivityTx) error) error {
	tenant, environment, err := allocationKey(key)
	if err != nil {
		return err
	}
	return translate(s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		owned, err := q.GetEnvironment(ctx, sqlc.GetEnvironmentParams{TenantID: tenant, ID: environment})
		if errors.Is(err, pgx.ErrNoRows) {
			// sessions.ErrNotFound keeps the public 404 that writeStoreError maps.
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		session := owned.Environment.SessionID
		locked, err := sessionpg.LockSession(ctx, q, tenant, session)
		if err != nil {
			return err
		}
		touch := activityTx(func() error {
			return q.TouchRuntimeActivity(ctx, sqlc.TouchRuntimeActivityParams{TenantID: tenant, EnvironmentID: environment})
		})
		if err := apply(locked, touch); err != nil {
			return err
		}
		return sessionpg.PruneChanges(ctx, q, session)
	}))
}

// activityTx is one Session-locked activity change.
type activityTx func() error

func (t activityTx) TouchActivity() error { return t() }

// reservationTx is one Session-locked allocation reservation.
type reservationTx struct {
	*sessionpg.SessionTx
	ctx                          context.Context
	q                            *sqlc.Queries
	tenant, environment, session pgtype.UUID
}

func (t *reservationTx) FindAllocation() (deployment.Allocation, bool, error) {
	row, err := t.q.GetRuntimeAllocation(t.ctx, sqlc.GetRuntimeAllocationParams{TenantID: t.tenant, EnvironmentID: t.environment})
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.Allocation{}, false, nil
	}
	if err != nil {
		return deployment.Allocation{}, false, err
	}
	return allocation(row.RuntimeAllocation, t.session, t.tenant, row.DeletedAt, row.Expired), true, nil
}

func (t *reservationTx) LockDeployment() (placement.Deployment, error) {
	return placementpg.LockDeployment(t.ctx, t.q)
}

func (t *reservationTx) LoadReserved() (placement.Reserved, error) {
	return placementpg.LoadReserved(t.ctx, t.q, t.environment)
}

func (t *reservationTx) InsertAllocation(a deployment.NewAllocation) (deployment.Allocation, error) {
	id, err := parseID(a.ID)
	if err != nil {
		return deployment.Allocation{}, err
	}
	device, err := parseID(a.DeviceID)
	if err != nil {
		return deployment.Allocation{}, err
	}
	provider, err := parseID(a.ProviderKey)
	if err != nil {
		return deployment.Allocation{}, err
	}
	node, err := lifecycleNode(a.NodeID)
	if err != nil {
		return deployment.Allocation{}, err
	}
	row, err := t.q.CreateRuntimeAllocation(t.ctx, sqlc.CreateRuntimeAllocationParams{
		ID: id, EnvironmentID: t.environment, DeviceID: device, ProviderKey: provider, NodeID: node,
		DeploymentGeneration: pgtype.Int8{Int64: int64(a.Generation), Valid: true},
		ProtocolVersion:      sandbox.SuspensionStateVersion,
	})
	if err != nil {
		return deployment.Allocation{}, err
	}
	return allocation(row, t.session, t.tenant, pgtype.Timestamptz{}, false), nil
}

// allocationTx is one allocation change in the transaction that locked the
// owning Session.
type allocationTx struct {
	ctx                          context.Context
	q                            *sqlc.Queries
	tenant, environment, session pgtype.UUID
}

func (t *allocationTx) LoadAllocation() (deployment.Allocation, error) {
	row, err := t.q.GetRuntimeAllocation(t.ctx, sqlc.GetRuntimeAllocationParams{TenantID: t.tenant, EnvironmentID: t.environment})
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.Allocation{}, deployment.ErrNotFound
	}
	if err != nil {
		return deployment.Allocation{}, err
	}
	return allocation(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, row.Expired), nil
}

func (t *allocationTx) LoadSessionDevice() (deployment.SessionDevice, bool, error) {
	row, err := t.q.GetSessionDevice(t.ctx, sqlc.GetSessionDeviceParams{TenantID: t.tenant, ID: t.session})
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.SessionDevice{}, false, nil
	}
	if err != nil {
		return deployment.SessionDevice{}, false, err
	}
	return deployment.SessionDevice{ID: uuidString(row.ID), EnvironmentID: uuidString(row.EnvironmentID)}, true, nil
}

func (t *allocationTx) LoadActivity(current deployment.Allocation) (deployment.Activity, error) {
	id, err := parseID(current.ID)
	if err != nil {
		return deployment.Activity{}, err
	}
	return loadActivity(t.ctx, t.q, id)
}

func (t *allocationTx) LoadRestore(current deployment.Allocation) (placement.Restore, error) {
	node, err := parseID(current.NodeID)
	if err != nil {
		return placement.Restore{}, err
	}
	return placementpg.LoadRestore(t.ctx, t.q, node, current.DeploymentGeneration)
}

func (t *allocationTx) ObserveRunning(current deployment.Allocation) (deployment.Allocation, error) {
	return t.change(current, t.q.ObserveRuntimeRunning)
}

func (t *allocationTx) Keep(current deployment.Allocation) (deployment.Allocation, error) {
	return t.change(current, t.q.KeepRuntimeAllocation)
}

func (t *allocationTx) SettleCreation(current deployment.Allocation) (deployment.Allocation, error) {
	return t.change(current, t.q.SettleRuntimeCreation)
}

func (t *allocationTx) Release(current deployment.Allocation) (deployment.Allocation, error) {
	released, err := t.change(current, t.q.ReleaseRuntimeAllocation)
	if err != nil {
		return deployment.Allocation{}, err
	}
	if err := placementpg.ReleasePlacement(t.ctx, t.q, t.environment); err != nil {
		return deployment.Allocation{}, err
	}
	return released, nil
}

func (t *allocationTx) SetCompute(current deployment.Allocation, change deployment.ComputeChange) (deployment.Allocation, error) {
	until := pgtype.Timestamptz{}
	if change.RetainedUntil != nil {
		until = pgtype.Timestamptz{Time: *change.RetainedUntil, Valid: true}
	}
	return t.change(current, func(ctx context.Context, id pgtype.UUID) (sqlc.RuntimeAllocation, error) {
		return t.q.SetRuntimeCompute(ctx, sqlc.SetRuntimeComputeParams{ID: id, Revision: current.ComputeRevision, Phase: change.Phase, State: change.State, RetainedUntil: until})
	})
}

func (t *allocationTx) RecordObservation(current deployment.Allocation, diagnostic string) error {
	id, err := parseID(current.ID)
	if err != nil {
		return err
	}
	return t.q.SetRuntimeObservation(t.ctx, sqlc.SetRuntimeObservationParams{ID: id, ComputeRevision: current.ComputeRevision, State: current.State, ObservationError: diagnostic})
}

// change applies a guarded write to current and returns the stored result
// with current's Session facts. A guard that no longer holds is
// ErrAllocationConflict.
func (t *allocationTx) change(current deployment.Allocation, write func(context.Context, pgtype.UUID) (sqlc.RuntimeAllocation, error)) (deployment.Allocation, error) {
	id, err := parseID(current.ID)
	if err != nil {
		return deployment.Allocation{}, err
	}
	row, err := write(t.ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.Allocation{}, deployment.ErrAllocationConflict
	}
	if err != nil {
		return deployment.Allocation{}, err
	}
	changed := allocation(row, t.session, t.tenant, pgtype.Timestamptz{}, current.Expired)
	changed.SessionDeleted = current.SessionDeleted
	return changed, nil
}

// cleanupTx is one allocation cleanup, with the owning Session's cancellation
// and Environment termination bound to the same transaction.
type cleanupTx struct {
	*allocationTx
	*sessionpg.SessionTx
}

func (t *cleanupTx) RevokeDevice(current deployment.Allocation) error {
	device, err := parseID(current.DeviceID)
	if err != nil {
		return err
	}
	_, err = t.q.RevokeRuntimeCleanupDevice(t.ctx, sqlc.RevokeRuntimeCleanupDeviceParams{TenantID: t.tenant, DeviceID: device})
	return err
}

func (t *cleanupTx) RequestCleanup(current deployment.Allocation) (deployment.Allocation, error) {
	return t.change(current, t.q.RequestRuntimeCleanup)
}

func loadActivity(ctx context.Context, q *sqlc.Queries, id pgtype.UUID) (deployment.Activity, error) {
	row, err := q.GetRuntimeActivity(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.Activity{}, deployment.ErrNotFound
	}
	if err != nil {
		return deployment.Activity{}, err
	}
	return deployment.Activity{LastActivity: row.LastActivity.Time, ObservedAt: row.ObservedAt.Time, Busy: row.Busy, WakeRequested: row.ComputeWakeRequested}, nil
}

func (s *Store) EnvironmentAllocation(ctx context.Context, key deployment.AllocationKey) (deployment.Allocation, error) {
	tenant, environment, err := allocationKey(key)
	if err != nil {
		return deployment.Allocation{}, err
	}
	row, err := s.pool.Queries().GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: tenant, EnvironmentID: environment})
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.Allocation{}, deployment.ErrNotFound
	}
	if err != nil {
		return deployment.Allocation{}, err
	}
	return allocation(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, row.Expired), nil
}

func (s *Store) CredentialAllocations(ctx context.Context, after string) ([]deployment.Allocation, error) {
	id, err := cursor(after)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Queries().ListRuntimeAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]deployment.Allocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, allocation(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, row.Expired))
	}
	return result, nil
}

func (s *Store) ObservationSessions(ctx context.Context, after string, limit int) (deployment.ObservationSessionPage, error) {
	if limit < 1 || limit > 100 {
		return deployment.ObservationSessionPage{}, deployment.ErrInvalidInput
	}
	id, err := cursor(after)
	if err != nil {
		return deployment.ObservationSessionPage{}, err
	}
	rows, err := s.pool.Queries().ListRuntimeObservationSessions(ctx, sqlc.ListRuntimeObservationSessionsParams{ID: id, Limit: int32(limit + 1)})
	if err != nil {
		return deployment.ObservationSessionPage{}, err
	}
	page := deployment.ObservationSessionPage{Sessions: make([]deployment.ObservationSession, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuidString(rows[limit-1].ID)
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Sessions = append(page.Sessions, deployment.ObservationSession{TenantID: uuidString(row.TenantID), SessionID: uuidString(row.ID)})
	}
	return page, nil
}

func (s *Store) NodeAllocations(ctx context.Context, nodeID string) ([]deployment.NodeAllocation, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return nil, err
	}
	q := s.pool.Queries()
	if _, err := q.GetRuntimeNode(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return nil, deployment.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := q.ListNodeRuntimeAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]deployment.NodeAllocation, 0, len(rows))
	for _, a := range rows {
		result = append(result, deployment.NodeAllocation{
			DeploymentGeneration: uint64(a.DeploymentGeneration.Int64), Diagnostic: a.ObservationError, ID: uuidString(a.ID), NodeID: uuidString(a.NodeID),
			TenantID: uuidString(a.TenantID), SessionID: uuidString(a.SessionID), EnvironmentID: uuidString(a.EnvironmentID), State: a.State,
			ComputePhase: a.ComputePhase, ComputePhaseChangedAt: timestamp(a.ComputePhaseChangedAt), Initialization: a.Initialization, CreatedAt: a.CreatedAt.Time,
		})
	}
	return result, nil
}

func (s *Store) NodeOnline(ctx context.Context, nodeID string) (bool, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return false, err
	}
	nodes, err := s.pool.Queries().ListRuntimeNodes(ctx, pgtype.UUID{})
	if err != nil {
		return false, err
	}
	for _, n := range nodes {
		if n.ID == id {
			return n.Online, nil
		}
	}
	return false, nil
}

// LifecycleNodes, LifecycleAllocations, UnallocatedEnvironments and
// LifecyclePlacement bound their reads by the execution deadline: the
// execution owner's lifecycles read through them before provider work.

func (s *Store) LifecycleNodes(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	rows, err := s.pool.Queries().ListRuntimeLifecycleNodes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(rows))
	for _, id := range rows {
		result = append(result, uuidString(id))
	}
	return result, nil
}

func (s *Store) LifecycleAllocations(ctx context.Context, nodeID, after string) ([]deployment.Allocation, error) {
	node, err := lifecycleNode(nodeID)
	if err != nil {
		return nil, err
	}
	id, err := cursor(after)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	rows, err := s.pool.Queries().ListRuntimeAllocationsForNode(ctx, sqlc.ListRuntimeAllocationsForNodeParams{NodeID: node, AfterID: id})
	if err != nil {
		return nil, err
	}
	result := make([]deployment.Allocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, allocation(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, row.Expired))
	}
	return result, nil
}

func (s *Store) UnallocatedEnvironments(ctx context.Context, nodeID, after string) ([]deployment.UnallocatedEnvironment, error) {
	node, err := lifecycleNode(nodeID)
	if err != nil {
		return nil, err
	}
	id, err := cursor(after)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	rows, err := s.pool.Queries().ListUnallocatedHostedEnvironmentsForNode(ctx, sqlc.ListUnallocatedHostedEnvironmentsForNodeParams{NodeID: node, AfterID: id})
	if err != nil {
		return nil, err
	}
	result := make([]deployment.UnallocatedEnvironment, 0, len(rows))
	for _, row := range rows {
		result = append(result, deployment.UnallocatedEnvironment{ID: uuidString(row.ID), TenantID: uuidString(row.TenantID)})
	}
	return result, nil
}

func (s *Store) LifecyclePlacement(ctx context.Context, key deployment.AllocationKey) (deployment.LifecyclePlacement, error) {
	tenant, environment, err := allocationKey(key)
	if err != nil {
		return deployment.LifecyclePlacement{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, pgunit.ExecutionTimeout)
	defer cancel()
	row, err := s.pool.Queries().GetRuntimeLifecyclePlacement(ctx, sqlc.GetRuntimeLifecyclePlacementParams{TenantID: tenant, ID: environment})
	if errors.Is(err, pgx.ErrNoRows) {
		// sessions.ErrNotFound keeps the public 404 that writeStoreError maps.
		return deployment.LifecyclePlacement{}, sessions.ErrNotFound
	}
	if err != nil {
		return deployment.LifecyclePlacement{}, err
	}
	return deployment.LifecyclePlacement{Provider: row.ProviderKind, Mode: row.Mode, AllocationID: uuidString(row.AllocationID), AllocationNodeID: uuidString(row.AllocationNodeID),
		PlacementNodeID: uuidString(row.PlacementNodeID), PlacementReleased: row.ReleasedAt.Valid}, nil
}

func (s *Store) Activity(ctx context.Context, allocationID string) (deployment.Activity, error) {
	id, err := parseID(allocationID)
	if err != nil {
		return deployment.Activity{}, err
	}
	return loadActivity(ctx, s.pool.Queries(), id)
}

func (s *Store) CountComputeReservations(ctx context.Context, installationID string) (int64, error) {
	id, err := parseID(installationID)
	if err != nil {
		return 0, err
	}
	return s.pool.Queries().CountRuntimeComputeReservations(ctx, id)
}

func (s *Store) CountRetainedAllocations(ctx context.Context, installationID string) (int64, error) {
	id, err := parseID(installationID)
	if err != nil {
		return 0, err
	}
	return s.pool.Queries().CountRuntimeRetainedAllocations(ctx, id)
}
