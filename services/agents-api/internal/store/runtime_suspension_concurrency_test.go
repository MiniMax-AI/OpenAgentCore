package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runtimeSuspensionLockedSession(t *testing.T, pool *pgxpool.Pool, session string) (context.Context, pgx.Tx, int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	var blocker int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM sessions WHERE id=$1 FOR UPDATE", session).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	return ctx, tx, blocker
}

func runtimeSuspensionWaitBlocked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker int32, done <-chan error) {
	t.Helper()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatal("operation bypassed Session lock", err)
		case <-ctx.Done():
			t.Fatal("Session lock wait not observed")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestRuntimeSuspensionClaimReadsPhaseAfterSessionLock(t *testing.T) {
	for _, phase := range []string{"disabled", "running", "quiescing", "suspending", "suspended", "restoring", "waking"} {
		t.Run(phase, func(t *testing.T) {
			s, w, pool, owner := runtimeSuspensionFixture(t)
			turn := uuid.NewString()
			runtimeSuspensionSQL(t, pool, `INSERT INTO turns(id,session_id,status) VALUES($1,$2,'queued')`, turn, owner.SessionID)
			ctx, tx, blocker := runtimeSuspensionLockedSession(t, pool, owner.SessionID)
			done := make(chan error, 1)
			go func() {
				_, err := w.TransitionTurn(ctx, owner.TenantID, owner.SessionID, turn, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress})
				done <- err
			}()
			runtimeSuspensionWaitBlocked(t, ctx, pool, blocker, done)
			// The lifecycle commits while a stale dispatcher is waiting to claim.
			if _, err := tx.Exec(ctx, `UPDATE runtime_allocations SET compute_phase=$2,compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID, phase); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err := <-done
			blocked := phase != "disabled" && phase != "running"
			if blocked && !errors.Is(err, ErrTurnConflict) || !blocked && err != nil {
				t.Fatal("incorrect claim outcome", phase, err)
			}
			got, err := s.GetTurn(ctx, owner.TenantID, owner.SessionID, turn)
			if err != nil {
				t.Fatal(err)
			}
			var startedEvents int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM session_events WHERE session_id=$1 AND payload->'event'->>'type'='agent.session.turn.in_progress'`, owner.SessionID).Scan(&startedEvents); err != nil {
				t.Fatal(err)
			}
			if blocked && (got.Status != TurnQueued || !got.StartedAt.IsZero() || startedEvents != 0) {
				t.Fatal("blocked claim changed Turn or projected start", got, startedEvents)
			}
			if !blocked && (got.Status != TurnInProgress || got.StartedAt.IsZero() || startedEvents != 1) {
				t.Fatal("ordinary compute no longer starts work", got, startedEvents)
			}
		})
	}
}

func TestRuntimeSuspensionPromotionRetainsPendingInput(t *testing.T) {
	s, w, pool, owner := runtimeSuspensionFixture(t)
	runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_phase='waking',compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID)
	pending := reserveEnvironmentInput(t, s, owner.TenantID, owner.SessionID, "during-wake")
	if _, err := w.PromoteEnvironmentInput(t.Context(), owner.TenantID, owner.SessionID, pending.ID); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("waking allocation promoted model input", err)
	}
	got, err := s.GetEnvironmentInputReservation(t.Context(), owner.TenantID, owner.SessionID, pending.ID)
	if err != nil || got.State != EnvironmentInputPending || got.SettledAt != nil || len(got.Receipts) != 0 {
		t.Fatal("blocked promotion partially committed", got, err)
	}
	environmentInputHistory(t, pool, owner.SessionID, 0, 0)
	runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_phase='running',compute_retained_until=NULL WHERE id=$1`, owner.ID)
	got, err = w.PromoteEnvironmentInput(t.Context(), owner.TenantID, owner.SessionID, pending.ID)
	if err != nil || got.State != EnvironmentInputAdmitted || len(got.Receipts) != 2 {
		t.Fatal("pending request could not resume once running", got, err)
	}
}

func TestRuntimeSuspensionCaptureRechecksNewPendingWork(t *testing.T) {
	for _, kind := range []string{"queued", "input", "file_write", "wake"} {
		t.Run(kind, func(t *testing.T) {
			s, w, pool, owner := runtimeSuspensionFixture(t)
			runtimeSuspensionCompleted(t, pool, owner)
			until := time.Now().Add(time.Hour)
			owner = runtimeSuspensionStep(t, w, owner, "quiescing", &until)
			ctx, tx, blocker := runtimeSuspensionLockedSession(t, pool, owner.SessionID)
			done := make(chan error, 1)
			go func() {
				_, err := w.SetRuntimeCompute(ctx, owner, "suspending", json.RawMessage(`{}`), &until, time.Time{})
				done <- err
			}()
			runtimeSuspensionWaitBlocked(t, ctx, pool, blocker, done)
			var err error
			switch kind {
			case "queued":
				_, err = tx.Exec(ctx, `INSERT INTO turns(id,session_id,status) VALUES($1,$2,'queued')`, uuid.NewString(), owner.SessionID)
			case "input":
				_, err = tx.Exec(ctx, `INSERT INTO environment_input_reservations(id,session_id,idempotency_key,batch,created_at,deadline) VALUES($1,$2,'pending','[{}]',clock_timestamp(),clock_timestamp()+interval '1 minute')`, uuid.NewString(), owner.SessionID)
			case "file_write":
				_, err = tx.Exec(ctx, `INSERT INTO environment_file_writes(id,environment_id,device_id,request_sha256) VALUES($1,$2,$3,$4)`, uuid.NewString(), owner.EnvironmentID, owner.DeviceID, strings.Repeat("a", 64))
			case "wake":
				_, err = tx.Exec(ctx, `UPDATE runtime_allocations SET compute_wake_requested=true,compute_activity_at=clock_timestamp() WHERE id=$1`, owner.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, ErrTurnConflict) {
				t.Fatal("capture ignored work admitted after quiesce", err)
			}
			got, err := s.GetRuntimeAllocation(ctx, owner.TenantID, owner.EnvironmentID)
			if err != nil || got.ComputePhase != "quiescing" || got.ComputeRevision != owner.ComputeRevision {
				t.Fatal("failed capture CAS changed its owner", got, err)
			}
		})
	}
}

func TestRuntimeSuspensionWakeUsesSessionLock(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "phase_commit", true: "deletion"}[deleted], func(t *testing.T) {
			s, _, pool, owner := runtimeSuspensionFixture(t)
			ctx, tx, blocker := runtimeSuspensionLockedSession(t, pool, owner.SessionID)
			done := make(chan error, 1)
			go func() { done <- s.TouchRuntimeActivity(ctx, owner.TenantID, owner.EnvironmentID) }()
			runtimeSuspensionWaitBlocked(t, ctx, pool, blocker, done)
			if deleted {
				if _, err := tx.Exec(ctx, `UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1`, owner.SessionID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := tx.Exec(ctx, `UPDATE runtime_allocations SET compute_phase='suspending',compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err := <-done
			if deleted && !errors.Is(err, ErrNotFound) || !deleted && err != nil {
				t.Fatal("wake did not observe locked state", err)
			}
			got, err := s.GetRuntimeAllocation(ctx, owner.TenantID, owner.EnvironmentID)
			if err != nil || got.ComputeWakeRequested == deleted {
				t.Fatal("wake was lost or crossed deletion", got, err)
			}
		})
	}
}

func TestRuntimeSuspensionQuiesceCannotOvertakeClaim(t *testing.T) {
	s, w, pool, owner := runtimeSuspensionFixture(t)
	runtimeSuspensionCompleted(t, pool, owner)
	until := time.Now().Add(time.Hour)
	ctx, tx, blocker := runtimeSuspensionLockedSession(t, pool, owner.SessionID)
	done := make(chan error, 1)
	go func() {
		_, err := w.SetRuntimeCompute(ctx, owner, "quiescing", json.RawMessage(`{}`), &until, time.Now())
		done <- err
	}()
	runtimeSuspensionWaitBlocked(t, ctx, pool, blocker, done)
	turn := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO turns(id,session_id,status) VALUES($1,$2,'queued')`, turn, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	params, err := turnLookup(owner.TenantID, owner.SessionID, turn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transitionTurn(ctx, w.queries.WithTx(tx), params, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress, Outcome: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrTurnConflict) {
		t.Fatal("quiesce overtook an admitted Turn", err)
	}
	got, err := s.GetRuntimeAllocation(ctx, owner.TenantID, owner.EnvironmentID)
	if err != nil || got.ComputePhase != "running" || got.ComputeRevision != owner.ComputeRevision {
		t.Fatal("active work was quiesced", got, err)
	}
}
