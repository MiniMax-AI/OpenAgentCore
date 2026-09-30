package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RuntimeActivity separates real work from the connection keepalive.
type RuntimeActivity struct {
	LastActivity, ObservedAt              time.Time
	Busy, WakeRequested, HasCompletedTurn bool
}

// ReadyToSuspend evaluates the idle policy using one database-clock observation.
func (a RuntimeActivity) ReadyToSuspend(idleTimeout time.Duration) bool {
	return idleTimeout > 0 && a.HasCompletedTurn && !a.Busy && !a.WakeRequested &&
		a.ObservedAt.Sub(a.LastActivity) >= idleTimeout
}

func runtimeActivity(row sqlc.GetRuntimeActivityRow) RuntimeActivity {
	return RuntimeActivity{LastActivity: row.LastActivity.Time, ObservedAt: row.ObservedAt.Time, Busy: row.Busy, WakeRequested: row.ComputeWakeRequested, HasCompletedTurn: row.HasCompletedTurn}
}

// SetRuntimeCompute commits an operation phase before its external effects.
// Revision and the existing Session lock fence a stale lifecycle observation.
func (s *Store) SetRuntimeCompute(ctx context.Context, owner RuntimeAllocation, phase string, state json.RawMessage, retainedUntil *time.Time, idleTimeout time.Duration) (RuntimeAllocation, error) {
	if !runtimeComputeTransition(owner.ComputePhase, phase) || !json.Valid(state) || (owner.ComputePhase == "running" && phase == "quiescing" && idleTimeout <= 0) {
		return RuntimeAllocation{}, ErrInvalidInput
	}
	if phase != "running" && (retainedUntil == nil || retainedUntil.IsZero()) {
		return RuntimeAllocation{}, ErrInvalidInput
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(state, &object) != nil || object == nil {
		return RuntimeAllocation{}, ErrInvalidInput
	}
	return s.mutateRuntimeAllocation(ctx, owner, true, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		if row.ComputeRevision != owner.ComputeRevision || row.ComputePhase != owner.ComputePhase {
			return sqlc.RuntimeAllocation{}, ErrTurnConflict
		}
		if (phase == "quiescing" && row.ComputePhase == "running") || (phase == "suspending" && row.ComputePhase == "quiescing") {
			activity, err := q.GetRuntimeActivity(ctx, row.ID)
			if err != nil {
				return sqlc.RuntimeAllocation{}, err
			}
			if activity.Busy || activity.ComputeWakeRequested {
				return sqlc.RuntimeAllocation{}, ErrTurnConflict
			}
			if phase == "quiescing" && (!runtimeActivity(activity).ReadyToSuspend(idleTimeout) || row.ComputeActivityAt.Time.After(owner.ComputeActivityAt)) {
				return sqlc.RuntimeAllocation{}, ErrTurnConflict
			}
		}
		if row.ComputePhase == "suspended" && phase == "restoring" {
			if err := reserveRuntimeRestore(ctx, q, row.NodeID, row.DeploymentGeneration); err != nil {
				return sqlc.RuntimeAllocation{}, err
			}
		}
		until := pgtype.Timestamptz{}
		if retainedUntil != nil {
			until = pgtype.Timestamptz{Time: *retainedUntil, Valid: true}
		}
		return q.SetRuntimeCompute(ctx, sqlc.SetRuntimeComputeParams{
			ID: row.ID, Revision: row.ComputeRevision, Phase: phase, State: state, RetainedUntil: until,
		})
	})
}

func runtimeComputeTransition(from, to string) bool {
	if from == to {
		return from != "disabled"
	}
	switch from {
	case "disabled":
		return to == "running"
	case "running":
		return to == "quiescing"
	case "quiescing":
		return to == "suspending" || to == "waking" || to == "running"
	case "suspending":
		return to == "suspended" || to == "waking"
	case "suspended":
		return to == "restoring"
	case "restoring":
		return to == "waking"
	case "waking":
		return to == "running"
	}
	return false
}

func (s *Store) RuntimeActivity(ctx context.Context, owner RuntimeAllocation) (RuntimeActivity, error) {
	id, err := parseID(owner.ID)
	if err != nil {
		return RuntimeActivity{}, err
	}
	if err := s.CheckExecutionOwnership(ctx); err != nil {
		return RuntimeActivity{}, err
	}
	row, err := s.queries.GetRuntimeActivity(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeActivity{}, ErrNotFound
	}
	if err != nil {
		return RuntimeActivity{}, err
	}
	return runtimeActivity(row), nil
}

// TouchRuntimeActivity is used only by operations requiring live compute.
// Public history and published-artifact reads do not call it.
func (s *Store) TouchRuntimeActivity(ctx context.Context, tenant, environment string) error {
	lookup, err := deviceLookup(tenant, environment)
	if err != nil {
		return err
	}
	owned, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return err
	}
	return s.withPublicSession(ctx, tenant, owned.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		current, err := q.GetEnvironment(ctx, sqlc.GetEnvironmentParams{TenantID: lookup.TenantID, ID: lookup.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current.Environment.SessionID != session {
			return ErrNotFound
		}
		return q.TouchRuntimeActivity(ctx, sqlc.TouchRuntimeActivityParams{TenantID: lookup.TenantID, EnvironmentID: lookup.ID})
	})
}

func (s *Store) ClearRuntimeWake(ctx context.Context, owner RuntimeAllocation, observedActivity time.Time) error {
	id, err := parseID(owner.ID)
	if err != nil {
		return err
	}
	if err := s.CheckExecutionOwnership(ctx); err != nil {
		return err
	}
	return s.queries.ClearRuntimeWake(ctx, sqlc.ClearRuntimeWakeParams{ID: id, ComputeActivityAt: pgtype.Timestamptz{Time: observedActivity, Valid: true}})
}

func (s *Store) CountRuntimeComputeReservations(ctx context.Context, provider string) (int64, error) {
	id, err := parseID(provider)
	if err != nil {
		return 0, err
	}
	if err := s.CheckExecutionOwnership(ctx); err != nil {
		return 0, err
	}
	return s.queries.CountRuntimeComputeReservations(ctx, id)
}

func (s *Store) CountRuntimeRetainedAllocations(ctx context.Context, provider string) (int64, error) {
	id, err := parseID(provider)
	if err != nil {
		return 0, err
	}
	if err := s.CheckExecutionOwnership(ctx); err != nil {
		return 0, err
	}
	return s.queries.CountRuntimeRetainedAllocations(ctx, id)
}

// checkRuntimeComputeAdmission runs under the Session lock before a new durable
// execution owner is created. Existing receipts remain readable in every phase.
func checkRuntimeComputeAdmission(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	blocked, err := q.RuntimeComputeBlocksAdmission(ctx, session)
	if err == nil && blocked {
		return ErrTurnConflict
	}
	return err
}
