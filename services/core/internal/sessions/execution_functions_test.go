package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// fakeExecutionStorage is strict lease-bound Session storage: a method whose
// func is unset fails the test. The Environment family's With methods record
// their call on tx, then run apply with tx and locked; without tx they fail
// the test.
type fakeExecutionStorage struct {
	t                *testing.T
	withFunctionTurn func(ctx context.Context, tenant, session, turn string, apply func(context.Context, FunctionTx, Turn) error) error
	withTurnJournal  func(ctx context.Context, tenant, session string, apply func(context.Context, TurnJournalTx) error) error

	tx     *fakeTx
	locked LockedSession
	// pages are the pages ListEnvironmentConnections reads, one per call.
	pages [][]EnvironmentKey
	// connections is the error each WithConnection call returns in place of
	// applying, by Environment.
	connections map[string]error
}

var _ ExecutionStorage = (*fakeExecutionStorage)(nil)

func (f *fakeExecutionStorage) WithFunctionTurn(ctx context.Context, tenant, session, turn string, apply func(context.Context, FunctionTx, Turn) error) error {
	f.t.Helper()
	if f.withFunctionTurn == nil {
		f.t.Fatal("unexpected call to WithFunctionTurn")
	}
	return f.withFunctionTurn(ctx, tenant, session, turn, apply)
}

func (f *fakeExecutionStorage) WithTurnJournal(ctx context.Context, tenant, session string, apply func(context.Context, TurnJournalTx) error) error {
	f.t.Helper()
	if f.withTurnJournal == nil {
		f.t.Fatal("unexpected call to WithTurnJournal")
	}
	return f.withTurnJournal(ctx, tenant, session, apply)
}

const testTenant = "4f0d0c35-8b8e-4d7c-9d1c-1f0a5b8a2e61"

// functionOperations serves the tenant's Turn as current through tx.
func functionOperations(t *testing.T, tx *fakeFunctionTx, current Turn) *ExecutionOperations {
	t.Helper()
	operations, err := NewExecutionOperations(&fakeExecutionStorage{t: t, withFunctionTurn: func(ctx context.Context, tenant, session, turn string, apply func(context.Context, FunctionTx, Turn) error) error {
		if tenant != testTenant || session != testSession || turn != testTurn {
			t.Fatalf("Turn %s %s %s", tenant, session, turn)
		}
		return apply(ctx, tx, current)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

// unusedStorage fails the test on any storage call.
func unusedStorage(t *testing.T) *ExecutionOperations {
	operations, err := NewExecutionOperations(&fakeExecutionStorage{t: t})
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

func TestNewExecutionOperationsRequiresStorage(t *testing.T) {
	if _, err := NewExecutionOperations(nil); err == nil {
		t.Fatal("nil storage accepted")
	}
}

func TestDecideFunctionCall(t *testing.T) {
	for name, test := range map[string]struct {
		turn   Turn
		match  FunctionCallMatch
		record bool
		err    error
	}{
		"new call":                 {turnWith(TurnInProgress), FunctionCallMatch{}, true, nil},
		"new call while waiting":   {turnWith(TurnWaiting), FunctionCallMatch{}, true, nil},
		"retry":                    {turnWith(TurnWaiting), FunctionCallMatch{Recorded: true, Matches: true}, false, nil},
		"retry after the Turn end": {turnWith(TurnCompleted), FunctionCallMatch{Recorded: true, Matches: true}, false, nil},
		"changed call":             {turnWith(TurnWaiting), FunctionCallMatch{Recorded: true}, false, ErrIdempotencyConflict},
		"queued Turn":              {turnWith(TurnQueued), FunctionCallMatch{}, false, ErrTurnConflict},
		"cancelling Turn":          {cancelling(TurnInProgress), FunctionCallMatch{}, false, ErrTurnConflict},
		"ended Turn":               {turnWith(TurnCancelled), FunctionCallMatch{}, false, ErrTurnConflict},
	} {
		record, err := decideFunctionCall(test.turn, test.match)
		if record != test.record || !errors.Is(err, test.err) || (test.err == nil && err != nil) {
			t.Errorf("%s: %v %v", name, record, err)
		}
	}
}

func TestDecideFunctionConfirmation(t *testing.T) {
	submitted := FunctionCall{CallID: "call", Result: json.RawMessage(`{"success":true}`)}
	applied := submitted
	applied.Applied = true
	for name, test := range map[string]struct {
		turn  Turn
		call  FunctionCall
		apply bool
		err   error
	}{
		"submitted":             {turnWith(TurnWaiting), submitted, true, nil},
		"already applied":       {turnWith(TurnWaiting), applied, false, nil},
		"applied after the end": {turnWith(TurnCompleted), applied, false, nil},
		"no result":             {turnWith(TurnWaiting), FunctionCall{CallID: "call"}, false, ErrTurnConflict},
		"cancelling":            {cancelling(TurnWaiting), submitted, false, ErrTurnConflict},
		"ended":                 {turnWith(TurnFailed), submitted, false, ErrTurnConflict},
	} {
		apply, err := decideFunctionConfirmation(test.turn, test.call)
		if apply != test.apply || !errors.Is(err, test.err) || (test.err == nil && err != nil) {
			t.Errorf("%s: %v %v", name, apply, err)
		}
	}
}

func TestRecordFunctionCall(t *testing.T) {
	usage := json.RawMessage(`{"input_tokens":1}`)
	t.Run("first call moves the Turn to waiting", func(t *testing.T) {
		running, waiting := turnWith(TurnInProgress), turnWith(TurnWaiting)
		var changes []SessionChange
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t, loadUsage: returns(usage), appendChanges: collect(&changes)},
			matchFunctionCall: returns(FunctionCallMatch{}), createFunctionCall: done,
			loadPendingFunctionCalls: returns([]FunctionCall{testCall}), applyFunctionTurnStatus: returns(waiting)}
		if err := functionOperations(t, f, running).RecordFunctionCall(t.Context(), testTenant, testSession, testTurn, testCall); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f.fakeTx, "MatchFunctionCall "+testTurn+" call", "CreateFunctionCall "+testTurn+" call", "LoadPendingFunctionCalls "+testTurn,
			"ApplyFunctionTurnStatus "+testTurn+" in_progress waiting", "AppendChanges ", "LoadUsage", "AppendChanges agent.session.requires_action")
		actions, _ := LoadRequiredActions(t.Context(), &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadPendingFunctionCalls: returns([]FunctionCall{testCall})}, waiting)
		if !reflect.DeepEqual(changes, []SessionChange{ActivityChange(waiting, usage, actions)}) {
			t.Fatalf("changes %+v", changes)
		}
	})
	t.Run("another call keeps the Turn waiting", func(t *testing.T) {
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t, loadUsage: returns(usage), appendChanges: func([]SessionChange) error { return nil }},
			matchFunctionCall: returns(FunctionCallMatch{}), createFunctionCall: done, loadPendingFunctionCalls: returns([]FunctionCall{testCall, testCall})}
		if err := functionOperations(t, f, turnWith(TurnWaiting)).RecordFunctionCall(t.Context(), testTenant, testSession, testTurn, testCall); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f.fakeTx, "MatchFunctionCall "+testTurn+" call", "CreateFunctionCall "+testTurn+" call", "LoadPendingFunctionCalls "+testTurn,
			"LoadUsage", "AppendChanges agent.session.requires_action")
	})
	t.Run("retry records nothing", func(t *testing.T) {
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, matchFunctionCall: returns(FunctionCallMatch{Recorded: true, Matches: true})}
		if err := functionOperations(t, f, turnWith(TurnWaiting)).RecordFunctionCall(t.Context(), testTenant, testSession, testTurn, testCall); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f.fakeTx, "MatchFunctionCall "+testTurn+" call")
	})
	t.Run("conflicts record nothing", func(t *testing.T) {
		for name, test := range map[string]struct {
			turn  Turn
			match FunctionCallMatch
			want  error
		}{
			"changed call": {turnWith(TurnWaiting), FunctionCallMatch{Recorded: true}, ErrIdempotencyConflict},
			"queued Turn":  {turnWith(TurnQueued), FunctionCallMatch{}, ErrTurnConflict},
		} {
			f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, matchFunctionCall: returns(test.match)}
			if err := functionOperations(t, f, test.turn).RecordFunctionCall(t.Context(), testTenant, testSession, testTurn, testCall); !errors.Is(err, test.want) {
				t.Errorf("%s: %v", name, err)
			}
			assertCalls(t, f.fakeTx, "MatchFunctionCall "+testTurn+" call")
		}
	})
	t.Run("an invalid call reaches no storage", func(t *testing.T) {
		call := testCall
		call.Name = ""
		if err := unusedStorage(t).RecordFunctionCall(t.Context(), testTenant, testSession, testTurn, call); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	})
	t.Run("storage errors", func(t *testing.T) {
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, matchFunctionCall: returns(FunctionCallMatch{}), createFunctionCall: func() error { return errStorage }}
		if err := functionOperations(t, f, turnWith(TurnInProgress)).RecordFunctionCall(t.Context(), testTenant, testSession, testTurn, testCall); !errors.Is(err, errStorage) {
			t.Fatal(err)
		}
	})
}

func TestConfirmFunctionResult(t *testing.T) {
	usage := json.RawMessage(`{"input_tokens":1}`)
	submitted := FunctionCall{CallID: "call", Result: json.RawMessage(`{"success":true}`)}
	t.Run("last receipt resumes the Turn", func(t *testing.T) {
		waiting, running := turnWith(TurnWaiting), turnWith(TurnInProgress)
		var changes []SessionChange
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t, loadUsage: returns(usage), appendChanges: collect(&changes)},
			loadFunctionCall: func() (FunctionCall, bool, error) { return submitted, true, nil }, applyFunctionResult: done,
			loadPendingFunctionCalls: returns([]FunctionCall{}), applyFunctionTurnStatus: returns(running)}
		if err := functionOperations(t, f, waiting).ConfirmFunctionResult(t.Context(), testTenant, testSession, testTurn, "call"); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f.fakeTx, "LoadFunctionCall "+testTurn+" call", "ApplyFunctionResult "+testTurn+" call", "LoadPendingFunctionCalls "+testTurn,
			"ApplyFunctionTurnStatus "+testTurn+" waiting in_progress", "AppendChanges agent.session.turn.in_progress", "LoadUsage", "AppendChanges agent.session.in_progress")
		want := append(TurnChanges(running, false), ActivityChange(running, usage, []v1.FunctionCallAction{}))
		if !reflect.DeepEqual(changes, want) {
			t.Fatalf("changes %+v", changes)
		}
	})
	t.Run("repeated receipt records nothing", func(t *testing.T) {
		applied := submitted
		applied.Applied = true
		f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadFunctionCall: func() (FunctionCall, bool, error) { return applied, true, nil }}
		if err := functionOperations(t, f, turnWith(TurnCompleted)).ConfirmFunctionResult(t.Context(), testTenant, testSession, testTurn, "call"); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f.fakeTx, "LoadFunctionCall "+testTurn+" call")
	})
	t.Run("rejections record nothing", func(t *testing.T) {
		for name, test := range map[string]struct {
			turn  Turn
			call  FunctionCall
			found bool
			want  error
		}{
			"missing call": {turnWith(TurnWaiting), FunctionCall{}, false, ErrNotFound},
			"no result":    {turnWith(TurnWaiting), FunctionCall{CallID: "call"}, true, ErrTurnConflict},
			"cancelling":   {cancelling(TurnWaiting), submitted, true, ErrTurnConflict},
		} {
			f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadFunctionCall: func() (FunctionCall, bool, error) { return test.call, test.found, nil }}
			if err := functionOperations(t, f, test.turn).ConfirmFunctionResult(t.Context(), testTenant, testSession, testTurn, "call"); !errors.Is(err, test.want) {
				t.Errorf("%s: %v", name, err)
			}
			assertCalls(t, f.fakeTx, "LoadFunctionCall "+testTurn+" call")
		}
	})
	t.Run("an invalid call reaches no storage", func(t *testing.T) {
		if err := unusedStorage(t).ConfirmFunctionResult(t.Context(), testTenant, testSession, testTurn, " "); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	})
}

func TestPendingFunctionCalls(t *testing.T) {
	f := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadPendingFunctionCalls: returns([]FunctionCall{testCall})}
	calls, err := functionOperations(t, f, turnWith(TurnWaiting)).PendingFunctionCalls(t.Context(), testTenant, testSession, testTurn)
	if err != nil || !reflect.DeepEqual(calls, []FunctionCall{testCall}) {
		t.Fatal(calls, err)
	}
	assertCalls(t, f.fakeTx, "LoadPendingFunctionCalls "+testTurn)

	// Pending calls are read whatever the Turn's status; the read itself
	// selects none for a cancelling or ended Turn.
	none := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadPendingFunctionCalls: returns([]FunctionCall(nil))}
	calls, err = functionOperations(t, none, turnWith(TurnCompleted)).PendingFunctionCalls(t.Context(), testTenant, testSession, testTurn)
	if err != nil || calls == nil || len(calls) != 0 {
		t.Fatal(calls, err)
	}

	failing := &fakeFunctionTx{fakeTx: &fakeTx{t: t}, loadPendingFunctionCalls: func() ([]FunctionCall, error) { return []FunctionCall{testCall}, errStorage }}
	if calls, err := functionOperations(t, failing, turnWith(TurnWaiting)).PendingFunctionCalls(t.Context(), testTenant, testSession, testTurn); !errors.Is(err, errStorage) || calls != nil {
		t.Fatal(calls, err)
	}
}
