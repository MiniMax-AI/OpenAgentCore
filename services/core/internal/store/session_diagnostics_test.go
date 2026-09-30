package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func diagnosticToolEvent(id, stage, status string) sessions.ExecutionEvent {
	return sessions.ExecutionEvent{Kind: "tool_call", Payload: json.RawMessage(fmt.Sprintf(`{"id":%q,"stage":%q,"observation":{"kind":"command","command":"private-command-canary","status":%q}}`, id, stage, status))}
}

func TestDiagnosticItemReceiptSettlementAndReplay(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	receipt := submitMessage(t, s, tenant, session.ID, "start")
	transition(t, s, tenant, session.ID, receipt.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	journal := sessionExecution(t, executionWriter(t, s).lease)
	before := diagnosticToolEvent("cmd", "before", "in_progress")
	after := diagnosticToolEvent("cmd", "after", "failed")
	for i, event := range []sessions.ExecutionEvent{before, after} {
		if err := journal.AppendTurnEvents(t.Context(), tenant, session.ID, receipt.TurnID, int32(i+1), []sessions.ExecutionEvent{event}); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
	if err != nil || len(snap.Items) != 2 {
		t.Fatal(snap, err)
	}
	if snap.Items[0].CompletedAt == nil || !snap.Items[0].CompletedAt.Equal(snap.Items[0].StartedAt) {
		t.Fatal("terminal input did not settle at its receipt", snap.Items[0])
	}
	item := snap.Items[1]
	var received time.Time
	if err := s.pool.QueryRow(t.Context(), "SELECT created_at FROM turn_events WHERE turn_id=$1 AND ordinal=2", receipt.TurnID).Scan(&received); err != nil {
		t.Fatal(err)
	}
	if item.CompletedAt == nil || !item.CompletedAt.Equal(received) || item.CompletedAt.Before(item.StartedAt) {
		t.Fatal("Item did not use terminal receipt", item, received)
	}
	if err := journal.AppendTurnEvents(t.Context(), tenant, session.ID, receipt.TurnID, 2, []sessions.ExecutionEvent{after}); err != nil {
		t.Fatal(err)
	}
	// A terminal legacy Item with unknown settlement must remain unknown even on a repeated upsert.
	runtimeSuspensionSQL(t, s.pool, "UPDATE session_items SET settled_at=NULL WHERE id=$1", item.ItemID)
	if err := journal.AppendTurnEvents(t.Context(), tenant, session.ID, receipt.TurnID, 3, []sessions.ExecutionEvent{after}); err != nil {
		t.Fatal(err)
	}
	transition(t, s, tenant, session.ID, receipt.TurnID, sessions.TurnInProgress, sessions.TurnFailed)
	snap, err = s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
	if err != nil || snap.Items[1].CompletedAt != nil {
		t.Fatal("historical settlement synthesized", snap, err)
	}
}

func TestDiagnosticForceSettlementIgnoresNativeClock(t *testing.T) {
	for _, skew := range []time.Duration{-269 * time.Second, 269 * time.Second} {
		t.Run(skew.String(), func(t *testing.T) {
			s, w, owner := managedIdleClockFixture(t)
			turn := uuid.NewString()
			runtimeSuspensionSQL(t, s.pool, "INSERT INTO turns(id,session_id,status,started_at) VALUES($1,$2,'in_progress',clock_timestamp())", turn, owner.SessionID)
			events := []sessions.ExecutionEvent{diagnosticToolEvent("first", "before", "in_progress"), diagnosticToolEvent("second", "before", "in_progress")}
			if err := sessionExecution(t, w.lease).AppendTurnEvents(t.Context(), owner.TenantID, owner.SessionID, turn, 1, events); err != nil {
				t.Fatal(err)
			}
			source := runtimeDatabaseTime(t, s).Add(skew).UnixMilli()
			before := runtimeDatabaseTime(t, s)
			completed, err := w.CompleteExecution(t.Context(), owner.TenantID, owner.SessionID, turn, sessions.TurnCompleted, json.RawMessage(fmt.Sprintf(`{"done":{"source_completed_at_ms":%d}}`, source)), "", 0)
			after := runtimeDatabaseTime(t, s)
			if err != nil || completed.CompletedAt.UnixMilli() != source {
				t.Fatal("public native completion changed", completed, err)
			}
			snap, err := s.GetTurnDiagnosticsSnapshot(t.Context(), owner.TenantID, owner.SessionID, turn)
			if err != nil || len(snap.Items) != 2 {
				t.Fatal(snap, err)
			}
			for _, item := range snap.Items {
				if item.CompletedAt == nil || item.CompletedAt.Before(before) || item.CompletedAt.After(after) || item.CompletedAt.Before(item.StartedAt) {
					t.Fatal("force settlement used remote or transaction-start clock", item)
				}
			}
			if !snap.Items[0].CompletedAt.Equal(*snap.Items[1].CompletedAt) {
				t.Fatal("force settlement has multiple clocks")
			}
		})
	}
}

func TestDiagnosticSettlementWaitsForSessionLock(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	receipt := submitMessage(t, s, tenant, session.ID, "start")
	transition(t, s, tenant, session.ID, receipt.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SELECT id FROM sessions WHERE id=$1 FOR UPDATE", session.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.TransitionTurn(t.Context(), tenant, session.ID, receipt.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnFailed})
		done <- err
	}()
	// Wait for the actual competing transaction to block, not a scheduler delay.
	deadline := time.After(5 * time.Second)
	for {
		var waiting bool
		err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%sessions%')").Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatal("terminal transaction did not wait", err)
		case <-deadline:
			t.Fatal("terminal transaction never reached lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	itemID := uuid.NewString()
	_, err = tx.Exec(t.Context(), "INSERT INTO session_items(id,session_id,turn_id,created_at,position,payload) VALUES($1,$2,$3,clock_timestamp(),1,$4)", itemID, session.ID, receipt.TurnID, `{"id":"`+itemID+`","turn_id":"`+receipt.TurnID+`","type":"command_execution","status":"in_progress","command":"run"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	snap, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	item := snap.Items[len(snap.Items)-1]
	if item.CompletedAt == nil || item.CompletedAt.Before(item.StartedAt) {
		t.Fatal("transaction-start settlement predates serialized Item receipt", item)
	}
}

func TestDiagnosticTimingBoundOrderAndIsolation(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	receipt := submitMessage(t, s, tenant, session.ID, "start")
	// Insert in reverse position order with equal times; response must follow public Item ordering.
	_, err := pool.Exec(t.Context(), `INSERT INTO session_items(id,session_id,turn_id,created_at,position,payload)
 SELECT gen_random_uuid(),$1,$2,'2020-01-01T00:00:00Z'::timestamptz,n,'{"status":"completed"}'::jsonb FROM generate_series(1000,1,-1) n`, session.ID, receipt.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
	if err != nil || len(snap.Items) != 1000 || !snap.ItemsTruncated {
		t.Fatal("unbounded diagnostics", len(snap.Items), snap.ItemsTruncated, err)
	}
	for i, item := range snap.Items {
		var position int
		if err := pool.QueryRow(t.Context(), "SELECT position FROM session_items WHERE id=$1", item.ItemID).Scan(&position); err != nil {
			t.Fatal(err)
		}
		if position != i+1 || item.CompletedAt != nil {
			t.Fatal("order or historical settlement changed", position, item)
		}
	}
	runtimeSuspensionSQL(t, pool, "DELETE FROM session_items WHERE turn_id=$1 AND created_at>'2020-01-01T00:00:00Z'", receipt.TurnID)
	exact, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
	if err != nil || len(exact.Items) != 1000 || exact.ItemsTruncated {
		t.Fatal("exact limit falsely truncated", len(exact.Items), exact.ItemsTruncated, err)
	}
	for _, ids := range [][3]string{{uuid.NewString(), session.ID, receipt.TurnID}, {tenant, "malformed", receipt.TurnID}, {tenant, session.ID, uuid.NewString()}} {
		if _, err := s.GetTurnDiagnosticsSnapshot(t.Context(), ids[0], ids[1], ids[2]); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal("scope leaked", ids, err)
		}
	}
	runtimeSuspensionSQL(t, pool, "UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1", session.ID)
	if _, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("deleted root visible", err)
	}
	if _, err := s.GetSessionDiagnosticsSnapshot(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("deleted Session visible", err)
	}
}

func TestDiagnosticProvisioningDetailAtomicAndPrivate(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	input := environmentInput("safe-detail", "openai_hosted", "/workspace")
	input.InitialInputs = []sessions.Input{messageInput("initial")}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	writer := executionWriter(t, s)
	owner, err := writer.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, uuid.NewString(), runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	constraint := "diagnostics_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = pool.Exec(t.Context(), "ALTER TABLE session_events ADD CONSTRAINT "+constraint+" CHECK (session_id <> '"+session.ID+"' OR payload->'event'->>'type' <> 'agent.session.failed') NOT VALID")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE session_events DROP CONSTRAINT IF EXISTS "+constraint)
	})
	failure := sessions.ProvisioningFailure{Step: sessions.ProvisioningSetupCommand, Index: 2, ExitCode: 7}
	runtimeSuspensionSQL(t, pool, "UPDATE environments SET initialization='running' WHERE id=$1", owner.EnvironmentID)
	preparation := sessions.EnvironmentInitialization{EnvironmentID: owner.EnvironmentID, SessionID: owner.SessionID, TenantID: owner.TenantID, DeviceID: owner.DeviceID}
	if err = sessionExecution(t, writer.lease).FailEnvironmentInitialization(t.Context(), preparation, failure); err == nil {
		t.Fatal("failure committed without events")
	}
	var detail []byte
	if err = pool.QueryRow(t.Context(), "SELECT failure_detail FROM environments WHERE id=$1", owner.EnvironmentID).Scan(&detail); err != nil || detail != nil {
		t.Fatal("partial failure detail", string(detail), err)
	}
	runtimeSuspensionSQL(t, pool, "ALTER TABLE session_events DROP CONSTRAINT "+constraint)
	if err = sessionExecution(t, writer.lease).FailEnvironmentInitialization(t.Context(), preparation, failure); err != nil {
		t.Fatal(err)
	}
	snap, err := s.GetSessionDiagnosticsSnapshot(t.Context(), tenant, session.ID)
	if err != nil || snap.EnvironmentFailure == nil || snap.EnvironmentFailure.Detail == nil || *snap.EnvironmentFailure.Detail.Index != 2 || *snap.EnvironmentFailure.Detail.ExitCode != 7 {
		t.Fatal("detail not persisted", snap.EnvironmentFailure, err)
	}
	events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "exit_code") || strings.Contains(string(raw), "failure_detail") {
		t.Fatal("private fields entered SSE", string(raw))
	}
	// Historical reasons are never parsed into structured detail; corrupted private fields are sanitized.
	runtimeSuspensionSQL(t, pool, "UPDATE environments SET failure_detail=$2 WHERE id=$1", owner.EnvironmentID, `{"step":"secret-provider-token","index":3,"exit_code":2}`)
	snap, err = s.GetSessionDiagnosticsSnapshot(t.Context(), tenant, session.ID)
	if err != nil || snap.EnvironmentFailure.Detail != nil {
		t.Fatal("unsafe private detail projected", snap.EnvironmentFailure, err)
	}
}

func TestDiagnosticPutItemRollsBack(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	receipt := submitMessage(t, s, tenant, session.ID, "start")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parseID(uuid.NewString())
	sid, _ := parseID(session.ID)
	tid, _ := parseID(receipt.TurnID)
	_, err = sqlc.New(tx).PutSessionItem(t.Context(), sqlc.PutSessionItemParams{ID: id, SessionID: sid, TurnID: tid, CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, Payload: []byte(`{"status":"completed"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
	if err != nil || len(snap.Items) != 1 {
		t.Fatal("rolled back receipt visible", snap, err)
	}
}

func TestDiagnosticFirstSettlementSurvivesStoredStatusRegression(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	receipt := submitMessage(t, s, tenant, session.ID, "start")
	transition(t, s, tenant, session.ID, receipt.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	events := []sessions.ExecutionEvent{diagnosticToolEvent("cmd", "before", "in_progress"), diagnosticToolEvent("cmd", "after", "completed")}
	journal := sessionExecution(t, executionWriter(t, s).lease)
	if err := journal.AppendTurnEvents(t.Context(), tenant, session.ID, receipt.TurnID, 1, events); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	item := first.Items[1]
	if item.CompletedAt == nil {
		t.Fatal("missing first settlement")
	}
	// Exercise persistence defensively even if a producer bypasses the normal
	// Item merge's terminal-status preservation.
	for _, force := range []bool{false, true} {
		runtimeSuspensionSQL(t, pool, "UPDATE session_items SET payload=jsonb_set(payload,'{status}','\"in_progress\"') WHERE id=$1", item.ItemID)
		if force {
			transition(t, s, tenant, session.ID, receipt.TurnID, sessions.TurnInProgress, sessions.TurnFailed)
		} else if err := journal.AppendTurnEvents(t.Context(), tenant, session.ID, receipt.TurnID, 3, events[1:]); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
		if err != nil || got.Items[1].CompletedAt == nil || !got.Items[1].CompletedAt.Equal(*item.CompletedAt) {
			t.Fatal("first settlement overwritten", force, got, err)
		}
	}
}

func TestDiagnosticRootReadRejectsActualChildTurn(t *testing.T) {
	s, w, owner := managedIdleClockFixture(t)
	root := runtimeSuspensionCompleted(t, s.pool, owner)
	child, turn := uuid.NewString(), uuid.NewString()
	runtimeSuspensionSQL(t, s.pool, `INSERT INTO turn_events(session_id,turn_id,ordinal,kind,payload) VALUES($1,$2,1,'subagent','{}')`, owner.SessionID, root)
	runtimeSuspensionSQL(t, s.pool, `INSERT INTO subagent_identities(id,session_id,device_id,engine,native_id,parent_native_id,native_created_at,first_turn_id,first_event_ordinal) VALUES($1,$2,$3,'codex','child','root',1,$4,1)`, child, owner.SessionID, owner.DeviceID, root)
	runtimeSuspensionSQL(t, s.pool, `INSERT INTO subagent_turns(id,session_id,subagent_id,native_id,status,created_at) VALUES($1,$2,$3,'child-turn','in_progress',clock_timestamp())`, turn, owner.SessionID, child)
	if _, err := w.GetTurnDiagnosticsSnapshot(t.Context(), owner.TenantID, owner.SessionID, turn); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("child Turn became root diagnostics", err)
	}
}

func TestDiagnosticSessionSnapshotConcurrentCommitConsistency(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	receipt := submitMessage(t, s, tenant, session.ID, "start")
	runtimeSuspensionSQL(t, pool, `UPDATE sessions SET metadata='{"generation":"0"}' WHERE id=$1`, session.ID)
	runtimeSuspensionSQL(t, pool, `UPDATE turns SET outcome='{"generation":"0"}' WHERE id=$1`, receipt.TurnID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		for i := 1; i <= 150; i++ {
			tx, err := pool.Begin(ctx)
			if err != nil {
				done <- err
				return
			}
			raw := fmt.Sprintf(`{"generation":"%d"}`, i)
			_, err = tx.Exec(ctx, "UPDATE sessions SET metadata=$2 WHERE id=$1", session.ID, raw)
			if err == nil {
				_, err = tx.Exec(ctx, "UPDATE turns SET outcome=$2 WHERE id=$1", receipt.TurnID, raw)
			}
			if err != nil {
				_ = tx.Rollback(context.Background())
				done <- err
				return
			}
			if err = tx.Commit(ctx); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 150; i++ {
		snapshot, err := s.GetSessionDiagnosticsSnapshot(t.Context(), tenant, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		var outcome map[string]string
		if snapshot.LastTurn == nil || json.Unmarshal(snapshot.LastTurn.Outcome, &outcome) != nil || outcome["generation"] != snapshot.Metadata["generation"] {
			t.Fatal("mixed committed snapshots", snapshot.Metadata, outcome)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
