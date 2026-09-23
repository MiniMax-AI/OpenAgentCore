package store

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runtimeSuspensionFixture(t *testing.T) (*Store, *Store, *pgxpool.Pool, RuntimeAllocation) {
	t.Helper()
	s, pool := testStore(t)
	w := executionLease(t, s).Store()
	tenant := uuid.NewString()
	_, environment := localEnvironment(t, s, tenant)
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, uuid.NewString(), device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.ObserveRuntimeRunning(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.SetRuntimeCompute(t.Context(), owner, "running", json.RawMessage(`{"instance":"original"}`), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return s, w, pool, owner
}

func runtimeSuspensionSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func runtimeSuspensionCompleted(t *testing.T, pool *pgxpool.Pool, owner RuntimeAllocation) string {
	t.Helper()
	id := uuid.NewString()
	runtimeSuspensionSQL(t, pool, `INSERT INTO turns(id,session_id,status,completed_at) VALUES($1,$2,'completed',clock_timestamp()-interval '10 minutes')`, id, owner.SessionID)
	return id
}

func runtimeSuspensionStep(t *testing.T, w *Store, owner RuntimeAllocation, phase string, until *time.Time) RuntimeAllocation {
	t.Helper()
	idleTimeout := time.Duration(0)
	if owner.ComputePhase == "running" && phase == "quiescing" {
		idleTimeout = time.Nanosecond
	}
	next, err := w.SetRuntimeCompute(t.Context(), owner, phase, json.RawMessage(`{"instance":"original","snapshot":"qualified"}`), until, idleTimeout)
	if err != nil {
		t.Fatalf("%s -> %s: %v", owner.ComputePhase, phase, err)
	}
	return next
}

func TestRuntimeSuspensionRequiresCompletedIdleAndNoPendingWork(t *testing.T) {
	cases := []string{"no_completed_turn", "queued", "in_progress", "waiting", "subagent_queued", "subagent_in_progress", "subagent_waiting", "input_reservation", "file_write", "idle"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			_, w, pool, owner := runtimeSuspensionFixture(t)
			completed := ""
			if kind != "no_completed_turn" {
				completed = runtimeSuspensionCompleted(t, pool, owner)
			}
			switch kind {
			case "queued", "in_progress", "waiting":
				runtimeSuspensionSQL(t, pool, `INSERT INTO turns(id,session_id,status) VALUES($1,$2,$3)`, uuid.NewString(), owner.SessionID, kind)
			case "subagent_queued", "subagent_in_progress", "subagent_waiting":
				child := uuid.NewString()
				runtimeSuspensionSQL(t, pool, `INSERT INTO turn_events(session_id,turn_id,ordinal,kind,payload) VALUES($1,$2,1,'subagent','{}')`, owner.SessionID, completed)
				runtimeSuspensionSQL(t, pool, `INSERT INTO subagent_identities(id,session_id,device_id,engine,native_id,parent_native_id,native_created_at,first_turn_id,first_event_ordinal) VALUES($1,$2,$3,'codex','child','root',1,$4,1)`, child, owner.SessionID, owner.DeviceID, completed)
				runtimeSuspensionSQL(t, pool, `INSERT INTO subagent_turns(id,session_id,subagent_id,native_id,status,created_at) VALUES($1,$2,$3,'child-turn',$4,clock_timestamp())`, uuid.NewString(), owner.SessionID, child, strings.TrimPrefix(kind, "subagent_"))
			case "input_reservation":
				runtimeSuspensionSQL(t, pool, `INSERT INTO environment_input_reservations(id,session_id,idempotency_key,batch,created_at,deadline) VALUES($1,$2,'pending','[{}]',clock_timestamp(),clock_timestamp()+interval '1 minute')`, uuid.NewString(), owner.SessionID)
			case "file_write":
				runtimeSuspensionSQL(t, pool, `INSERT INTO environment_file_writes(id,environment_id,device_id,request_sha256) VALUES($1,$2,$3,$4)`, uuid.NewString(), owner.EnvironmentID, owner.DeviceID, strings.Repeat("a", 64))
			}
			activity, err := w.RuntimeActivity(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			wantBusy := kind != "idle" && kind != "no_completed_turn"
			if activity.Busy != wantBusy || activity.HasCompletedTurn != (kind != "no_completed_turn") {
				t.Fatalf("activity lost pending work: %+v", activity)
			}
			until := time.Now().Add(time.Hour)
			_, err = w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, time.Nanosecond)
			if kind == "idle" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrTurnConflict) {
				t.Fatalf("unsafe quiesce admitted: %v", err)
			}
		})
	}
}

func TestRuntimeSuspensionCASAndActivityFence(t *testing.T) {
	s, w, pool, owner := runtimeSuspensionFixture(t)
	runtimeSuspensionCompleted(t, pool, owner)
	until := time.Now().Add(time.Hour)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{"operation":"same-observation"}`), &until, time.Nanosecond)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrTurnConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("CAS admitted competing owners: wins=%d conflicts=%d", winners, conflicts)
	}
	current, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ComputeRevision != owner.ComputeRevision+1 || current.ComputePhase != "quiescing" {
		t.Fatal("operation intent not durable", current)
	}
	if _, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{"stale":true}`), &until, time.Nanosecond); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("stale phase overwrite", err)
	}
	current = runtimeSuspensionStep(t, w, current, "running", nil)
	observed := current
	if err := s.TouchRuntimeActivity(t.Context(), owner.TenantID, owner.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	// Clearing an observed wake cannot let an older observation authorize sleep.
	latest, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ClearRuntimeWake(t.Context(), latest, latest.ComputeActivityAt); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SetRuntimeCompute(t.Context(), observed, "quiescing", json.RawMessage(`{}`), &until, time.Nanosecond); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("newer activity was swallowed", err)
	}
	for _, invalid := range []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`null`), json.RawMessage(`false`), json.RawMessage(`{`)} {
		if _, err := w.SetRuntimeCompute(t.Context(), latest, "running", invalid, nil, 0); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("non-object compute state accepted", string(invalid), err)
		}
	}
	if _, err := w.SetRuntimeCompute(t.Context(), latest, "suspended", json.RawMessage(`{}`), &until, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("running skipped snapshot protocol", err)
	}
}

func TestRuntimeSuspensionWakeDoesNotLoseNewerWork(t *testing.T) {
	s, w, pool, owner := runtimeSuspensionFixture(t)
	runtimeSuspensionCompleted(t, pool, owner)
	before, err := w.RuntimeActivity(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.KeepRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(t.Context(), owner.TenantID, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	quiet, err := w.RuntimeActivity(t.Context(), owner)
	if err != nil || quiet.WakeRequested || !quiet.LastActivity.Equal(before.LastActivity) {
		t.Fatal("heartbeat or history read touched compute activity", quiet, err)
	}
	if err := s.TouchRuntimeActivity(t.Context(), uuid.NewString(), owner.EnvironmentID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	foreign, err := w.RuntimeActivity(t.Context(), owner)
	if err != nil || foreign.WakeRequested {
		t.Fatal("foreign tenant woke environment", foreign, err)
	}
	if err := s.TouchRuntimeActivity(t.Context(), owner.TenantID, owner.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TouchRuntimeActivity(t.Context(), owner.TenantID, owner.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	second, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil || !second.ComputeActivityAt.After(first.ComputeActivityAt) {
		t.Fatal("Touch did not record newer activity", err)
	}
	if err := w.ClearRuntimeWake(t.Context(), first, first.ComputeActivityAt); err != nil {
		t.Fatal(err)
	}
	activity, err := w.RuntimeActivity(t.Context(), owner)
	if err != nil || !activity.WakeRequested {
		t.Fatal("old wake receipt swallowed newer work", activity, err)
	}
	if err := w.ClearRuntimeWake(t.Context(), second, second.ComputeActivityAt); err != nil {
		t.Fatal(err)
	}
	activity, err = w.RuntimeActivity(t.Context(), owner)
	if err != nil || activity.WakeRequested {
		t.Fatal("current wake receipt did not settle", activity, err)
	}
}

func TestRuntimeSuspensionRetentionAndDeletedSession(t *testing.T) {
	s, w, pool, owner := runtimeSuspensionFixture(t)
	runtimeSuspensionCompleted(t, pool, owner)
	until := time.Now().Add(time.Hour)
	for _, phase := range []string{"quiescing", "suspending", "suspended"} {
		owner = runtimeSuspensionStep(t, w, owner, phase, &until)
	}
	runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET kept_at=clock_timestamp()-interval '2 hours' WHERE id=$1`, owner.ID)
	retained, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil || retained.Expired {
		t.Fatal("suspended snapshot expired by disconnected heartbeat", retained, err)
	}
	runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID)
	expired, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil || !expired.Expired {
		t.Fatal("snapshot retention expiry not observed", expired, err)
	}
	// Use the earlier unexpired observation to exercise expiry at the database CAS.
	if _, err := w.SetRuntimeCompute(t.Context(), retained, "restoring", json.RawMessage(`{}`), &until, 0); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("expired snapshot restored from stale observation", err)
	}
	if err := s.DeleteSession(t.Context(), owner.TenantID, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchRuntimeActivity(t.Context(), owner.TenantID, owner.EnvironmentID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	deleted, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil || !deleted.SessionDeleted || deleted.ComputeWakeRequested {
		t.Fatal("deleted session was woken", deleted, err)
	}
	if _, err := w.SetRuntimeCompute(t.Context(), deleted, "restoring", json.RawMessage(`{}`), &until, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted session restored", err)
	}
}

func TestRuntimeSuspensionCountsUncertainCapacityUntilReleased(t *testing.T) {
	s, pool := testStore(t)
	w := executionLease(t, s).Store()
	provider := uuid.NewString()
	cases := []struct {
		state, phase string
		count        bool
	}{
		{"creating", "disabled", true}, {"running", "running", true}, {"running", "quiescing", true}, {"running", "suspending", true}, {"running", "suspended", false}, {"running", "restoring", true}, {"running", "waking", true}, {"cleanup_pending", "restoring", true}, {"released", "running", false},
	}
	want := int64(0)
	wantRetained := int64(0)
	for _, item := range cases {
		tenant := uuid.NewString()
		_, env := localEnvironment(t, s, tenant)
		owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, env.ID, provider, device.HashCredential(uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET state=$2,compute_phase=$3,create_settled=($2<>'creating'),released_at=CASE WHEN $2='released' THEN clock_timestamp() END WHERE id=$1`, owner.ID, item.state, item.phase)
		if item.count {
			want++
		}
		if item.state != "released" {
			wantRetained++
		}
		retained, err := w.CountRuntimeRetainedAllocations(t.Context(), provider)
		if err != nil || retained != wantRetained {
			t.Fatalf("retained capacity state=%s phase=%s got=%d want=%d err=%v", item.state, item.phase, retained, wantRetained, err)
		}
		got, err := w.CountRuntimeComputeReservations(t.Context(), provider)
		if err != nil || got != want {
			t.Fatalf("capacity state=%s phase=%s got=%d want=%d err=%v", item.state, item.phase, got, want, err)
		}
	}
	got, err := w.CountRuntimeComputeReservations(t.Context(), uuid.NewString())
	if err != nil || got != 0 {
		t.Fatal("capacity crossed installation boundary", got, err)
	}
}

func TestRuntimeSuspensionIdleStartsAfterLastCompletion(t *testing.T) {
	_, w, pool, owner := runtimeSuspensionFixture(t)
	turn := runtimeSuspensionCompleted(t, pool, owner)
	runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '2 hours' WHERE id=$1`, owner.ID)
	var completed time.Time
	if err := pool.QueryRow(t.Context(), `SELECT completed_at FROM turns WHERE id=$1`, turn).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	activity, err := w.RuntimeActivity(t.Context(), owner)
	if err != nil || !activity.LastActivity.Equal(completed) {
		t.Fatal("long Turn completion did not restart idle interval", activity, completed, err)
	}
}

func TestRuntimeSuspensionWakeRemainsUntilRunning(t *testing.T) {
	s, w, pool, owner := runtimeSuspensionFixture(t)
	runtimeSuspensionCompleted(t, pool, owner)
	until := time.Now().Add(time.Hour)
	for _, phase := range []string{"quiescing", "suspending", "suspended"} {
		owner = runtimeSuspensionStep(t, w, owner, phase, &until)
	}
	if err := s.TouchRuntimeActivity(t.Context(), owner.TenantID, owner.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	observed, err := s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ClearRuntimeWake(t.Context(), owner, observed.ComputeActivityAt); err != nil {
		t.Fatal(err)
	}
	activity, err := w.RuntimeActivity(t.Context(), owner)
	if err != nil || !activity.WakeRequested {
		t.Fatal("wake cleared before compute was running", activity, err)
	}
	for _, phase := range []string{"restoring", "waking"} {
		owner = runtimeSuspensionStep(t, w, owner, phase, &until)
	}
	owner = runtimeSuspensionStep(t, w, owner, "running", nil)
	if owner.ComputeRetainedUntil != nil {
		t.Fatal("running retained snapshot expiration")
	}
	if err := w.ClearRuntimeWake(t.Context(), owner, observed.ComputeActivityAt); err != nil {
		t.Fatal(err)
	}
	activity, err = w.RuntimeActivity(t.Context(), owner)
	if err != nil || activity.WakeRequested {
		t.Fatal("running compute could not settle wake", activity, err)
	}
}

func TestRuntimeSuspensionExpiredRunningAndLostWriterAreFenced(t *testing.T) {
	_, w, pool, owner := runtimeSuspensionFixture(t)
	runtimeSuspensionCompleted(t, pool, owner)
	until := time.Now().Add(time.Hour)
	runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET kept_at=clock_timestamp()-interval '2 hours' WHERE id=$1`, owner.ID)
	if _, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, time.Nanosecond); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("expired running allocation entered checkpoint", err)
	}
	if err := w.executionLease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RuntimeActivity(t.Context(), owner); err == nil {
		t.Fatal("lost writer read execution activity")
	}
	if err := w.ClearRuntimeWake(t.Context(), owner, owner.ComputeActivityAt); err == nil {
		t.Fatal("lost writer changed wake receipt")
	}
	if _, err := w.CountRuntimeComputeReservations(t.Context(), owner.ProviderKey); err == nil {
		t.Fatal("lost writer admitted running capacity")
	}
	if _, err := w.CountRuntimeRetainedAllocations(t.Context(), owner.ProviderKey); err == nil {
		t.Fatal("lost writer admitted retained capacity")
	}
}

func TestRuntimeSuspensionRechecksCompletionAgainstIdleTimeout(t *testing.T) {
	for _, kind := range []string{"root", "subagent", "file_committed", "file_rejected"} {
		t.Run(kind, func(t *testing.T) {
			s, w, pool, owner := runtimeSuspensionFixture(t)
			turn := runtimeSuspensionCompleted(t, pool, owner)
			runtimeSuspensionSQL(t, pool, `UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '20 minutes' WHERE id=$1`, owner.ID)
			var err error
			owner, err = s.GetRuntimeAllocation(t.Context(), owner.TenantID, owner.EnvironmentID)
			if err != nil {
				t.Fatal(err)
			}
			idleTimeout := time.Minute
			observed, err := w.RuntimeActivity(t.Context(), owner)
			if err != nil || !observed.ReadyToSuspend(idleTimeout) {
				t.Fatal("fixture is not initially idle", observed, err)
			}
			// A completion can arrive after the lifecycle's idle observation without
			// changing the allocation revision or its explicit wake timestamp.
			switch kind {
			case "root":
				runtimeSuspensionSQL(t, pool, `UPDATE turns SET completed_at=clock_timestamp() WHERE id=$1`, turn)
			case "subagent":
				child := uuid.NewString()
				runtimeSuspensionSQL(t, pool, `INSERT INTO turn_events(session_id,turn_id,ordinal,kind,payload) VALUES($1,$2,1,'subagent','{}')`, owner.SessionID, turn)
				runtimeSuspensionSQL(t, pool, `INSERT INTO subagent_identities(id,session_id,device_id,engine,native_id,parent_native_id,native_created_at,first_turn_id,first_event_ordinal) VALUES($1,$2,$3,'codex','child','root',1,$4,1)`, child, owner.SessionID, owner.DeviceID, turn)
				runtimeSuspensionSQL(t, pool, `INSERT INTO subagent_turns(id,session_id,subagent_id,native_id,status,created_at,completed_at) VALUES($1,$2,$3,'child-turn','completed',clock_timestamp()-interval '10 minutes',clock_timestamp())`, uuid.NewString(), owner.SessionID, child)
			default:
				runtimeSuspensionSQL(t, pool, `INSERT INTO environment_file_writes(id,environment_id,device_id,request_sha256,state,created_at,settled_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()-interval '10 minutes',clock_timestamp())`, uuid.NewString(), owner.EnvironmentID, owner.DeviceID, strings.Repeat("a", 64), strings.TrimPrefix(kind, "file_"))
			}
			until := time.Now().Add(time.Hour)
			if _, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, idleTimeout); !errors.Is(err, ErrTurnConflict) {
				t.Fatal("completion after idle observation did not fence quiesce", err)
			}
			activity, err := w.RuntimeActivity(t.Context(), owner)
			if err != nil || activity.Busy || activity.WakeRequested || activity.ReadyToSuspend(idleTimeout) {
				t.Fatal("last completion did not restart idle interval", activity, err)
			}
			if _, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 0); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("missing idle timeout accepted", err)
			}
			if _, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, time.Nanosecond); err != nil {
				t.Fatal("elapsed idle timeout rejected", err)
			}
		})
	}
}
