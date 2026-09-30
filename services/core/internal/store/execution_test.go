package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// executionWriter acquires the execution lease on the shared test database and
// builds s's execution writer on it, as cmd/server does. Tests in this package
// run sequentially, so one writer at a time owns it; writer.lease releases it.
func executionWriter(t *testing.T, s *Store) *Store {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), s.pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	return NewExecution(s, lease)
}

// sessionExecution builds the Session execution operations on lease, as
// cmd/server does.
func sessionExecution(t *testing.T, lease *pgunit.Lease) *sessions.ExecutionOperations {
	t.Helper()
	operations, err := sessions.NewExecutionOperations(sessionpg.NewExecution(lease))
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

// functionExecution builds the Session execution operations on the execution
// lease of a pool of its own, so the test may close its Store's pool.
func functionExecution(t *testing.T) *sessions.ExecutionOperations {
	t.Helper()
	other, _ := testStore(t)
	return sessionExecution(t, executionWriter(t, other).lease)
}

func functionCallFixture(id string) sessions.FunctionCall {
	return sessions.FunctionCall{CallID: id, ExecutorCallID: "native-" + id, Name: "lookup", Arguments: json.RawMessage(`{"ticket":9007199254740993}`)}
}

// executionOwnerPID finds the backend holding this database's execution lease,
// matching the single-bigint key in queries/scheduling.sql.
func executionOwnerPID(t *testing.T, pool *pgxpool.Pool) int32 {
	t.Helper()
	var pid int32
	err := pool.QueryRow(t.Context(), `SELECT pid FROM pg_locks WHERE locktype='advisory' AND granted AND objsubid=1
		AND classid::bigint * 4294967296 + objid::bigint = 706172736172
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&pid)
	if err != nil {
		t.Fatal("observe execution lease owner", err)
	}
	return pid
}

func TestExecutionLeaseLossFencesAllLifecycleWrites(t *testing.T) {
	s, pool := testStore(t)
	writer := executionWriter(t, s)
	functions := sessionExecution(t, writer.lease)
	tenant, active := newTurnSession(t, s)
	input := submitMessage(t, s, tenant, active.ID, "active")
	transition(t, writer, tenant, active.ID, input.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	host, err := s.CreateDevice(t.Context(), tenant, "owner test", runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.BindSessionDevice(t.Context(), tenant, active.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	queued, err := s.CreateSession(t.Context(), tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	pending := submitMessage(t, s, tenant, queued.ID, "queued")
	waiting, err := s.CreateSession(t.Context(), tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "waiting"})
	if err != nil {
		t.Fatal(err)
	}
	waitInput := submitMessage(t, s, tenant, waiting.ID, "waiting")
	transition(t, writer, tenant, waiting.ID, waitInput.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	call := functionCallFixture("saved")
	if err = functions.RecordFunctionCall(t.Context(), tenant, waiting.ID, waitInput.TurnID, call); err != nil {
		t.Fatal(err)
	}
	if err = SubmitFixtureFunctionResult(t.Context(), s, tenant, waiting.ID, waitInput.TurnID, call.CallID, json.RawMessage(`{"success":true,"output":"saved"}`)); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetSession(t.Context(), tenant, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := s.SessionEventCursor(t.Context(), tenant, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Kill only this test's owner connection. Do not notify the old writer by a check.
	var killed bool
	if err = pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", executionOwnerPID(t, pool)).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	successor := executionWriter(t, s)
	mustReject := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("stale %s committed while successor owned lease", name)
		}
	}
	mustReject("binding", writer.BindSessionDevice(t.Context(), tenant, queued.ID, host.ID))
	_, err = writer.TransitionTurn(t.Context(), tenant, queued.ID, pending.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
	mustReject("claim", err)
	mustReject("journal", writer.AppendTurnEvents(t.Context(), tenant, active.ID, input.TurnID, 1, []sessions.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"stale"}`)}}))
	mustReject("callback", functions.RecordFunctionCall(t.Context(), tenant, active.ID, input.TurnID, functionCallFixture("late")))
	mustReject("receipt", functions.ConfirmFunctionResult(t.Context(), tenant, waiting.ID, waitInput.TurnID, call.CallID))
	_, err = writer.CompleteExecution(t.Context(), tenant, active.ID, input.TurnID, sessions.TurnCompleted, json.RawMessage(`{"done":{"content":"stale"}}`), "stale-native", input.Sequence)
	mustReject("completion", err)
	_, err = writer.TransitionTurn(t.Context(), tenant, active.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnFailed})
	mustReject("reconciliation", err)
	_, err = writer.ExpireEnvironmentInputs(t.Context())
	mustReject("input expiry", err)
	mustReject("ownership check", writer.lease.CheckOwnership(t.Context()))
	after, err := s.GetSession(t.Context(), tenant, active.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("stale state persisted", after, err)
	}
	afterCursor, err := s.SessionEventCursor(t.Context(), tenant, active.ID)
	if err != nil || afterCursor != cursor {
		t.Fatal("stale events published", afterCursor, err)
	}
	events, err := s.ListTurnEvents(t.Context(), tenant, active.ID, input.TurnID, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal("stale journal persisted", events, err)
	}
	saved, err := FixtureFunctionCall(t.Context(), s.pool, tenant, waiting.ID, waitInput.TurnID, call.CallID)
	if err != nil || saved.Applied {
		t.Fatal("stale receipt persisted", saved, err)
	}
	if _, err = s.GetSessionDevice(t.Context(), tenant, queued.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("stale binding persisted", err)
	}
	queuedTurn, err := s.GetTurn(t.Context(), tenant, queued.ID, pending.TurnID)
	if err != nil || queuedTurn.Status != sessions.TurnQueued {
		t.Fatal("queued work changed", queuedTurn, err)
	}
	// Public admission remains usable with a dead owner connection.
	submitMessage(t, s, tenant, queued.ID, "additional")
	if err = successor.lease.CheckOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = successor.CompleteExecution(t.Context(), tenant, active.ID, input.TurnID, sessions.TurnCompleted, json.RawMessage(`{"done":{"content":"accepted"}}`), "successor-native", input.Sequence); err != nil {
		t.Fatal(err)
	}
	bound, err := s.GetSessionExecutionBinding(t.Context(), tenant, active.ID)
	if err != nil || bound.NativeSessionID != "successor-native" {
		t.Fatal(bound, err)
	}
	_, err = successor.TransitionTurn(t.Context(), tenant, active.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnFailed})
	if !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatal("terminal CAS changed", err)
	}
	if err = successor.lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	mustReject("closed writer", successor.BindSessionDevice(t.Context(), tenant, queued.ID, host.ID))
}

func TestExecutionWriterSerializesWritesOnItsLease(t *testing.T) {
	s, pool := testStore(t)
	writer := executionWriter(t, s)
	owner := executionOwnerPID(t, pool)
	backend := func(store *Store) int32 {
		t.Helper()
		var pid int32
		if err := store.writer.Transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid)
		}); err != nil {
			t.Fatal(err)
		}
		return pid
	}
	if backend(writer) != owner || backend(s) == owner {
		t.Fatal("execution transactions do not run on the leased connection")
	}
	type work struct{ tenant, session, turn string }
	tasks := make([]work, 4)
	for i := range tasks {
		tenant, session := newTurnSession(t, s)
		tasks[i] = work{tenant, session.ID, submitMessage(t, s, tenant, session.ID, "start").TurnID}
	}
	var group sync.WaitGroup
	results := make(chan error, len(tasks)*2)
	for _, task := range tasks {
		group.Go(func() {
			_, err := writer.TransitionTurn(t.Context(), task.tenant, task.session, task.turn, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
			if err == nil {
				err = writer.AppendTurnEvents(t.Context(), task.tenant, task.session, task.turn, 1, []sessions.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"accepted"}`)}})
			}
			results <- err
		})
		group.Go(func() { results <- writer.lease.CheckOwnership(t.Context()) })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	// While the owner connection is in use, public admission on another Session
	// does not need that connection.
	entered, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- writer.writer.Transaction(t.Context(), func(context.Context, pgx.Tx) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	task := tasks[0]
	_, err := s.SubmitMessage(ctx, task.tenant, task.session, "public", json.RawMessage(`{"text":"additional"}`))
	close(release)
	if err != nil {
		t.Fatal("public admission used owner gate", err)
	}
	if err := <-held; err != nil {
		t.Fatal(err)
	}
}

func TestPooledStoreHasNoExecutionAuthority(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	input := submitMessage(t, s, tenant, session.ID, "start")
	before, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, archiveErr := s.ArchiveManagedSession(t.Context(), tenant, session.ID, 0)
	_, expiryErr := s.ExpireEnvironmentInputs(t.Context())
	subagent := []sessions.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"visible"}`)}, subagentIdentityEvent("child", "root", 100)}
	for name, err := range map[string]error{
		"ownership check":   s.checkExecutionOwnership(t.Context()),
		"archive":           archiveErr,
		"input expiry":      expiryErr,
		"reconciliation":    s.ReconcileEnvironmentConnections(t.Context()),
		"mixed batch":       s.AppendTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 1, subagent),
		"subagent only":     s.AppendTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 1, subagent[1:]),
		"connection change": s.ReplaceEnvironmentConnection(t.Context(), tenant, uuid.NewString(), uuid.NewString()),
	} {
		if !errors.Is(err, ErrExecutionAuthority) {
			t.Fatalf("pooled Store ran %s: %v", name, err)
		}
	}
	after, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected execution operation changed the Session", after, err)
	}
	if next, err := s.SessionEventCursor(t.Context(), tenant, session.ID); err != nil || next != cursor {
		t.Fatal("rejected execution operation published events", next, err)
	}
	if events, err := s.ListTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 0, 100); err != nil || len(events) != 0 {
		t.Fatal("rejected batch reached the journal", events, err)
	}
}
