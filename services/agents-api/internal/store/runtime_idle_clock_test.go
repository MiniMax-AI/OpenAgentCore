package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func managedIdleClockFixture(t *testing.T) (*Store, *Store, RuntimeAllocation) {
	t.Helper()
	s, w, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	input := managerSessionInput("idle-clock", d.LocalNodeID)
	input.Configuration = json.RawMessage(`{"agent":{"id":"agent_root","model":"test","multi_agent":{"enabled":true}},"environment":{"type":"openai_hosted"}}`)
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, d.InstallationID, device.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.ObserveRuntimeRunning(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.SetRuntimeCompute(t.Context(), owner, "running", json.RawMessage(`{"instance":"original"}`), nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	runtimeSuspensionSQL(t, s.pool, "UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '10 minutes' WHERE id=$1", owner.ID)
	return s, w, owner
}
func runtimeDatabaseTime(t *testing.T, s *Store) time.Time {
	t.Helper()
	var now time.Time
	if err := s.pool.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	return now
}
func verifyManagedIdleClock(t *testing.T, s, w *Store, owner RuntimeAllocation, before, after time.Time) RuntimeActivity {
	t.Helper()
	activity, err := w.RuntimeActivity(t.Context(), owner)
	if err != nil || activity.LastActivity.Before(before) || activity.LastActivity.After(after) || activity.Busy || activity.WakeRequested || !activity.HasCompletedTurn {
		t.Fatal("idle clock did not use committed terminal ingestion", activity, before, after, err)
	}
	until := runtimeDatabaseTime(t, s).Add(time.Hour)
	if _, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, before.Add(-time.Second)); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("new completion admitted premature idle", err)
	}
	// Advance only the internal activity age; the remote public timestamp remains unchanged.
	runtimeSuspensionSQL(t, s.pool, "UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '2 seconds' WHERE id=$1", owner.ID)
	if _, err := w.SetRuntimeCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, runtimeDatabaseTime(t, s).Add(-time.Second)); err != nil {
		t.Fatal("remote timestamp delayed elapsed idle timer", err)
	}
	return activity
}
func TestManagedIdleClockIgnoresRootHostSkew(t *testing.T) {
	for _, skew := range []time.Duration{-269 * time.Second, 269 * time.Second} {
		t.Run(skew.String(), func(t *testing.T) {
			s, w, owner := managedIdleClockFixture(t)
			turn := uuid.NewString()
			runtimeSuspensionSQL(t, s.pool, "INSERT INTO turns(id,session_id,status,started_at) VALUES($1,$2,'in_progress',clock_timestamp())", turn, owner.SessionID)
			source := runtimeDatabaseTime(t, s).Add(skew).UnixMilli()
			outcome := json.RawMessage(fmt.Sprintf(`{"done":{"source_completed_at_ms":%d}}`, source))
			before := runtimeDatabaseTime(t, s)
			completed, err := w.CompleteExecution(t.Context(), owner.TenantID, owner.SessionID, turn, TurnCompleted, outcome, "", 0)
			after := runtimeDatabaseTime(t, s)
			if err != nil || completed.CompletedAt.UnixMilli() != source {
				t.Fatal("native completion changed or rejected", completed, err)
			}
			recorded, err := w.RuntimeActivity(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.CompleteExecution(t.Context(), owner.TenantID, owner.SessionID, turn, TurnCompleted, outcome, "", 0); !errors.Is(err, ErrTurnConflict) {
				t.Fatal("terminal replay accepted", err)
			}
			unchanged, err := w.RuntimeActivity(t.Context(), owner)
			if err != nil || !unchanged.LastActivity.Equal(recorded.LastActivity) {
				t.Fatal("terminal retry reset idle", unchanged, err)
			}
			verifyManagedIdleClock(t, s, w, owner, before, after)
			read, err := s.GetTurn(t.Context(), owner.TenantID, owner.SessionID, turn)
			if err != nil || read.CompletedAt.UnixMilli() != source {
				t.Fatal("public native timestamp rewritten", read, err)
			}
		})
	}
}
func TestManagedIdleClockIgnoresChildHostSkewAndReplay(t *testing.T) {
	for _, skew := range []time.Duration{-269 * time.Second, 269 * time.Second} {
		t.Run(skew.String(), func(t *testing.T) {
			s, w, owner := managedIdleClockFixture(t)
			root := runtimeSuspensionCompleted(t, s.pool, owner)
			child := uuid.NewString()
			runtimeSuspensionSQL(t, s.pool, `INSERT INTO turn_events(session_id,turn_id,ordinal,kind,payload) VALUES($1,$2,1,'subagent','{}')`, owner.SessionID, root)
			runtimeSuspensionSQL(t, s.pool, `INSERT INTO subagent_identities(id,session_id,device_id,engine,native_id,parent_native_id,native_created_at,first_turn_id,first_event_ordinal,public_visible) VALUES($1,$2,$3,'codex','child','root',1,$4,1,true)`, child, owner.SessionID, owner.DeviceID, root)
			source := runtimeDatabaseTime(t, s).Add(skew).UnixMilli()
			created := source - 1000
			payload, _ := json.Marshal(proto.SubagentTurnPayload{NativeID: "child", TurnID: "remote-turn", Status: TurnCompleted, CreatedAtMS: created, StartedAtMS: &created, CompletedAtMS: &source})
			project := func() error {
				return w.withSession(t.Context(), owner.TenantID, owner.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
					return projectSubagentTurn(ctx, q, session, payload)
				})
			}
			before := runtimeDatabaseTime(t, s)
			if err := project(); err != nil {
				t.Fatal(err)
			}
			after := runtimeDatabaseTime(t, s)
			recorded, err := w.RuntimeActivity(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := project(); err != nil {
				t.Fatal(err)
			}
			unchanged, err := w.RuntimeActivity(t.Context(), owner)
			if err != nil || !unchanged.LastActivity.Equal(recorded.LastActivity) {
				t.Fatal("replayed child completion reset idle", unchanged, err)
			}
			verifyManagedIdleClock(t, s, w, owner, before, after)
			page, err := s.ListSubagentTurns(t.Context(), owner.TenantID, owner.SessionID, child, "", 10, true)
			if err != nil || len(page.Data) != 1 {
				t.Fatal(page, err)
			}
			read, err := s.GetTurn(t.Context(), owner.TenantID, owner.SessionID, page.Data[0].ID)
			if err != nil || read.CompletedAt.UnixMilli() != source {
				t.Fatal("child native timestamp rewritten", read, err)
			}
		})
	}
}
func TestUnmanagedRootCompletionStillRejectsEarlierClock(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	input := submitMessage(t, s, tenant, session.ID, "earlier-clock")
	current := transition(t, s, tenant, session.ID, input.TurnID, TurnQueued, TurnInProgress)
	source := current.CreatedAt.Add(-269 * time.Second).UnixMilli()
	outcome := json.RawMessage(fmt.Sprintf(`{"done":{"source_completed_at_ms":%d}}`, source))
	if _, err := s.CompleteExecution(t.Context(), tenant, session.ID, input.TurnID, TurnCompleted, outcome, "", input.Sequence); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unmanaged timing contract changed", err)
	}
}
