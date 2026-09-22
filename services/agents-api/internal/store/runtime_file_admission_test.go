package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func runtimeFileWriteKey(owner RuntimeAllocation) FileWriteIdentity {
	return FileWriteIdentity{ID: uuid.NewString(), DeviceID: owner.DeviceID, RequestSHA256: strings.Repeat("a", 64)}
}

func TestRuntimeFileWriteRequiresRunningComputeBeforeNewIntent(t *testing.T) {
	for _, phase := range []string{"disabled", "running", "quiescing", "suspending", "suspended", "restoring", "waking"} {
		t.Run(phase, func(t *testing.T) {
			s, w, pool, owner := runtimeSuspensionFixture(t)
			runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_phase=$2,compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID, phase)
			key := runtimeFileWriteKey(owner)
			write, err := w.ReserveEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, key)
			blocked := phase != "disabled" && phase != "running"
			if blocked {
				if !errors.Is(err, ErrTurnConflict) {
					t.Fatal("suspended compute admitted a new write", write, err)
				}
				if _, err := s.GetEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, key.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("rejected admission retained a blocking intent", err)
				}
				runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_phase='running',compute_retained_until=NULL WHERE id=$1`, owner.ID)
				write, err = w.ReserveEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, key)
			}
			if err != nil || write.Replayed || write.State != "pending" {
				t.Fatal("running compute could not admit original request", write, err)
			}
			if _, err := w.SettleEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, key, "committed"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeFileWritePhaseFencePreservesExistingReceipts(t *testing.T) {
	_, w, pool, owner := runtimeSuspensionFixture(t)
	key := runtimeFileWriteKey(owner)
	first, err := w.ReserveEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, key)
	if err != nil {
		t.Fatal(err)
	}
	// A retry observes retained evidence even if its Runtime phase has changed;
	// it never grants permission to send the unknown write again.
	runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_phase='waking',compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID)
	for _, state := range []string{"pending", "committed"} {
		got, err := w.ReserveEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, key)
		if err != nil || !got.Replayed || got.State != state || got.Identity != key || !got.CreatedAt.Equal(first.CreatedAt) {
			t.Fatal("phase fence changed the existing receipt", got, err)
		}
		changed := key
		changed.RequestSHA256 = strings.Repeat("b", 64)
		if _, err := w.ReserveEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, changed); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal("phase fence masked changed retry identity", err)
		}
		if _, err := w.ReserveEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, runtimeFileWriteKey(owner)); !errors.Is(err, ErrTurnConflict) {
			t.Fatal("receipt authorized a successor while waking", err)
		}
		if state == "pending" {
			if _, err := w.SettleEnvironmentFileWrite(t.Context(), owner.TenantID, owner.EnvironmentID, key, "committed"); err != nil {
				t.Fatal("phase fence prevented exact receipt settlement", err)
			}
		}
	}
}

func TestRuntimeFileWriteAndQuiesceSerializeBothOrders(t *testing.T) {
	for _, first := range []string{"quiesce", "write"} {
		t.Run(first, func(t *testing.T) {
			s, w, pool, owner := runtimeSuspensionFixture(t)
			runtimeSuspensionCompleted(t, pool, owner)
			key := runtimeFileWriteKey(owner)
			until := time.Now().Add(time.Hour)
			ctx, tx, blocker := runtimeSuspensionLockedSession(t, pool, owner.SessionID)
			done := make(chan error, 1)
			go func() {
				var err error
				if first == "quiesce" {
					_, err = w.ReserveEnvironmentFileWrite(ctx, owner.TenantID, owner.EnvironmentID, key)
				} else {
					_, err = w.SetRuntimeCompute(ctx, owner, "quiescing", json.RawMessage(`{}`), &until, time.Now())
				}
				done <- err
			}()
			runtimeSuspensionWaitBlocked(t, ctx, pool, blocker, done)
			// Commit the first owner's durable change while the competitor waits
			// for the same Session lock, fixing the race order without sleeps.
			var err error
			if first == "quiesce" {
				_, err = tx.Exec(ctx, `UPDATE runtime_allocations SET compute_phase='quiescing',compute_revision=compute_revision+1,compute_retained_until=$2 WHERE id=$1`, owner.ID, until)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO environment_file_writes(id,environment_id,device_id,request_sha256) VALUES($1,$2,$3,$4)`, key.ID, owner.EnvironmentID, key.DeviceID, key.RequestSHA256)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, ErrTurnConflict) {
				t.Fatal("competing durable owner bypassed the phase fence", err)
			}
			allocation, err := s.GetRuntimeAllocation(ctx, owner.TenantID, owner.EnvironmentID)
			if err != nil {
				t.Fatal(err)
			}
			write, err := s.GetEnvironmentFileWrite(ctx, owner.TenantID, owner.EnvironmentID, key.ID)
			if first == "quiesce" {
				if allocation.ComputePhase != "quiescing" || !errors.Is(err, ErrNotFound) {
					t.Fatal("late write survived quiesce", allocation.ComputePhase, write, err)
				}
			} else if allocation.ComputePhase != "running" || err != nil || write.State != "pending" {
				t.Fatal("quiesce overtook pending write", allocation.ComputePhase, write, err)
			}
		})
	}
}
