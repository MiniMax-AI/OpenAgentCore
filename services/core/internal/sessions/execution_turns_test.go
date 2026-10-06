package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func TestDecideTransition(t *testing.T) {
	cancelling := turnWith(TurnWaiting)
	cancelling.CancelRequestedAt = time.Unix(1, 0)
	big := json.RawMessage(`{"x":"` + strings.Repeat("x", maxOutcomeBytes) + `"}`)
	for name, test := range map[string]struct {
		current    Turn
		transition TurnTransition
		want       TurnStatusChange
		err        error
	}{
		"start is admitted": {
			current: turnWith(TurnQueued), transition: TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress},
			want: TurnStatusChange{Expected: TurnQueued, Status: TurnInProgress, Outcome: json.RawMessage(`{}`)},
		},
		"wait keeps running": {
			current: turnWith(TurnInProgress), transition: TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnWaiting, Outcome: json.RawMessage(`{}`)},
			want: TurnStatusChange{Expected: TurnInProgress, Status: TurnWaiting, Outcome: json.RawMessage(`{}`)},
		},
		"failure ends": {
			current: turnWith(TurnInProgress), transition: TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed, Outcome: json.RawMessage(` {"error":"x"} `)},
			want: TurnStatusChange{Expected: TurnInProgress, Status: TurnFailed, Outcome: json.RawMessage(`{"error":"x"}`)},
		},
		"cancelled waiting Turn ends":   {current: cancelling, transition: TurnTransition{ExpectedStatus: TurnWaiting, Status: TurnCancelled}, want: TurnStatusChange{Expected: TurnWaiting, Status: TurnCancelled, Outcome: json.RawMessage(`{}`)}},
		"ended Turn never moves":        {current: turnWith(TurnCompleted), transition: TurnTransition{ExpectedStatus: TurnCompleted, Status: TurnFailed}, err: ErrInvalidInput},
		"skipped status":                {current: turnWith(TurnQueued), transition: TurnTransition{ExpectedStatus: TurnQueued, Status: TurnCompleted}, err: ErrInvalidInput},
		"oversized outcome":             {current: turnWith(TurnInProgress), transition: TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed, Outcome: big}, err: ErrInvalidInput},
		"outcome not an object":         {current: turnWith(TurnInProgress), transition: TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed, Outcome: json.RawMessage(`[]`)}, err: ErrInvalidInput},
		"outcome on a running Turn":     {current: turnWith(TurnInProgress), transition: TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnWaiting, Outcome: json.RawMessage(`{"a":1}`)}, err: ErrInvalidInput},
		"invalid before conflict":       {current: turnWith(TurnCompleted), transition: TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnWaiting, Outcome: json.RawMessage(`{"a":1}`)}, err: ErrInvalidInput},
		"status moved":                  {current: turnWith(TurnCompleted), transition: TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed}, err: ErrTurnConflict},
		"cancellation blocks resuming":  {current: cancelling, transition: TurnTransition{ExpectedStatus: TurnWaiting, Status: TurnInProgress}, err: ErrTurnConflict},
		"cancellation blocks the start": {current: func() Turn { turn := turnWith(TurnQueued); turn.CancelRequestedAt = time.Unix(1, 0); return turn }(), transition: TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress}, err: ErrTurnConflict},
	} {
		got, err := decideTransition(test.current, test.transition)
		if !errors.Is(err, test.err) {
			t.Fatalf("%s: %v, want %v", name, err, test.err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s: %+v, want %+v", name, got, test.want)
		}
	}
}

func TestDecideCompletion(t *testing.T) {
	for _, test := range []struct {
		current, status string
		err             error
	}{
		{TurnInProgress, TurnCompleted, nil},
		{TurnInProgress, TurnFailed, nil},
		{TurnInProgress, TurnCancelled, nil},
		{TurnWaiting, TurnFailed, nil},
		{TurnWaiting, TurnCancelled, nil},
		{TurnWaiting, TurnCompleted, ErrTurnConflict},
		{TurnQueued, TurnFailed, ErrTurnConflict},
		{TurnCompleted, TurnFailed, ErrTurnConflict},
	} {
		if err := decideCompletion(test.current, test.status); !errors.Is(err, test.err) {
			t.Fatalf("%s to %s: %v, want %v", test.current, test.status, err, test.err)
		}
	}
}

func TestSourceCompletedAt(t *testing.T) {
	for outcome, want := range map[string]struct {
		at  time.Time
		err error
	}{
		`{}`:          {},
		`{"done":{}}`: {},
		`{"done":{"source_completed_at_ms":1500}}`: {at: time.UnixMilli(1500)},
		`{"done":{"source_completed_at_ms":0}}`:    {err: ErrInvalidInput},
		`{"done":{"source_completed_at_ms":"x"}}`:  {err: ErrInvalidInput},
	} {
		at, err := sourceCompletedAt(json.RawMessage(outcome))
		if !errors.Is(err, want.err) || !at.Equal(want.at) {
			t.Fatalf("%s: %v %v, want %v %v", outcome, at, err, want.at, want.err)
		}
	}
}

// moves is a fake ApplyTurnStatus that returns the Turn in its new status.
func moves(change TurnStatusChange) (Turn, error) {
	turn := turnWith(change.Status)
	turn.Outcome = change.Outcome
	return turn, nil
}

// A queued Turn starts only after compute admission, and a Turn that keeps
// running journals its event.
func TestTransitionTurnStarts(t *testing.T) {
	f := &fakeTx{t: t, loadTurn: returns(turnWith(TurnQueued)), loadComputeSuspension: returns(false), applyTurnStatus: moves, appendChanges: func([]SessionChange) error { return nil }}
	turn, err := TransitionTurn(t.Context(), f, testTurn, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress})
	if err != nil || turn.Status != TurnInProgress {
		t.Fatal(turn, err)
	}
	assertCalls(t, f, "LoadTurn "+testTurn, "LoadComputeSuspension", "ApplyTurnStatus "+testTurn+" queued in_progress {}", "AppendChanges agent.session.turn.in_progress")

	f = &fakeTx{t: t, loadTurn: returns(turnWith(TurnQueued)), loadComputeSuspension: returns(true)}
	if _, err := TransitionTurn(t.Context(), f, testTurn, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress}); !errors.Is(err, ErrTurnConflict) {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadTurn "+testTurn, "LoadComputeSuspension")
}

// A Turn that ends projects its outcome, then settles from the Turn as the
// projection left it.
func TestTransitionTurnEnds(t *testing.T) {
	outcome := `{"done":{"usage":{"tokens":{"cached_input_tokens":0,"input_tokens":1,"output_tokens":2,"reasoning_output_tokens":0,"total_tokens":3}}}}`
	settled := turnWith(TurnFailed)
	settled.Usage = json.RawMessage(`{"total_tokens":3}`)
	read := []Turn{turnWith(TurnInProgress), settled}
	var end TurnEnd
	f := &fakeTx{
		t: t, loadTurn: func() (Turn, error) { turn := read[0]; read = read[1:]; return turn, nil }, applyTurnStatus: moves,
		putTurnUsage: func(v1.TokenUsage) error { return nil }, loadEnding: returns(Ending{}), applyTurnEnd: func(e TurnEnd) error { end = e; return nil },
	}
	turn, err := TransitionTurn(t.Context(), f, testTurn, TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed, Outcome: json.RawMessage(outcome)})
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadTurn "+testTurn, "ApplyTurnStatus "+testTurn+" in_progress failed "+outcome, "PutTurnUsage "+testTurn, "LoadTurn "+testTurn, "LoadEnding "+testTurn, "ApplyTurnEnd "+testTurn)
	if string(turn.Usage) != string(settled.Usage) || end.Artifacts != DiscardArtifacts {
		t.Fatal(turn, end)
	}
}

// A rejected transition writes nothing.
func TestTransitionTurnRejectsWithoutWrites(t *testing.T) {
	f := &fakeTx{t: t, loadTurn: returns(turnWith(TurnCompleted))}
	if _, err := TransitionTurn(t.Context(), f, testTurn, TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed}); !errors.Is(err, ErrTurnConflict) {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadTurn "+testTurn)

	f = &fakeTx{t: t, loadTurn: func() (Turn, error) { return Turn{}, ErrNotFound }}
	if _, err := TransitionTurn(t.Context(), f, testTurn, TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadTurn "+testTurn)
}

// turnOperations runs every Turn transaction of the tenant's Session on tx.
func turnOperations(t *testing.T, tx *fakeTx) *ExecutionOperations {
	t.Helper()
	operations, err := NewExecutionOperations(&fakeExecutionStorage{t: t, withTurns: func(ctx context.Context, tenant, session string, apply func(context.Context, TurnTx) error) error {
		if tenant != testTenant || session != testSession {
			t.Fatalf("Session %s %s", tenant, session)
		}
		return apply(ctx, tx)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

// A completed execution checks its inputs, moves the Turn with the source
// completion time, journals the outcome after the last entry, remembers the
// native session and settles the Turn, which publishes its Artifacts.
func TestCompleteExecutionOrder(t *testing.T) {
	outcome := `{"done":{"source_completed_at_ms":1500}}`
	var end TurnEnd
	f := &fakeTx{
		t: t, loadJournalTurn: func() (JournalTurn, bool, error) {
			return JournalTurn{Status: TurnInProgress, EventCount: 4}, true, nil
		},
		hasUnappliedInputs: returns(false), applyTurnStatus: moves, insertEvent: done, countEvents: done, loadEventSources: returns([]Source(nil)),
		rememberNativeSession: done, loadTurn: returns(turnWith(TurnCompleted)), loadEnding: returns(Ending{}),
		applyTurnEnd: func(e TurnEnd) error { end = e; return nil },
	}
	turn, err := turnOperations(t, f).CompleteExecution(t.Context(), testTenant, testSession, testTurn, TurnCompleted, json.RawMessage(outcome), "native-1", 7)
	if err != nil || turn.Status != TurnCompleted {
		t.Fatal(turn, err)
	}
	assertCalls(t, f,
		"LoadJournalTurn "+testTurn, "HasUnappliedInputs "+testTurn+" 7",
		"ApplyTurnStatus "+testTurn+" in_progress completed "+outcome+" 1500",
		"InsertEvent "+testTurn+" 5 execution_completed", "CountEvents "+testTurn+" 1 "+fmt.Sprint(len(outcome)), "LoadEventSources "+testTurn+" 5",
		"RememberNativeSession native-1", "LoadTurn "+testTurn, "LoadEnding "+testTurn, "ApplyTurnEnd "+testTurn,
	)
	if end.Artifacts != PublishArtifacts {
		t.Fatal(end)
	}
}

// A failed execution of a waiting Turn skips the input check and the native
// session, and settles the Turn, which discards its Artifacts.
func TestCompleteExecutionFails(t *testing.T) {
	var end TurnEnd
	f := &fakeTx{
		t: t, loadJournalTurn: func() (JournalTurn, bool, error) { return JournalTurn{Status: TurnWaiting}, true, nil },
		applyTurnStatus: moves, insertEvent: done, countEvents: done, loadEventSources: returns([]Source(nil)),
		loadTurn: returns(turnWith(TurnFailed)), loadEnding: returns(Ending{}), applyTurnEnd: func(e TurnEnd) error { end = e; return nil },
	}
	if _, err := turnOperations(t, f).CompleteExecution(t.Context(), testTenant, testSession, testTurn, TurnFailed, nil, "", 0); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadJournalTurn "+testTurn, "ApplyTurnStatus "+testTurn+" waiting failed {}", "InsertEvent "+testTurn+" 1 execution_failed", "CountEvents "+testTurn+" 1 2", "LoadEventSources "+testTurn+" 1", "LoadTurn "+testTurn, "LoadEnding "+testTurn, "ApplyTurnEnd "+testTurn)
	if end.Artifacts != DiscardArtifacts {
		t.Fatal(end)
	}
}

func TestCompleteExecutionRejects(t *testing.T) {
	for name, test := range map[string]struct {
		turn    JournalTurn
		found   bool
		pending bool
		outcome string
		err     error
		calls   []string
	}{
		"missing Turn":           {err: ErrNotFound, calls: []string{"LoadJournalTurn " + testTurn}},
		"waiting Turn completes": {turn: JournalTurn{Status: TurnWaiting}, found: true, err: ErrTurnConflict, calls: []string{"LoadJournalTurn " + testTurn}},
		"unapplied inputs":       {turn: JournalTurn{Status: TurnInProgress}, found: true, pending: true, err: ErrUnappliedInputs, calls: []string{"LoadJournalTurn " + testTurn, "HasUnappliedInputs " + testTurn + " 0"}},
		"bad source time":        {turn: JournalTurn{Status: TurnInProgress}, found: true, outcome: `{"done":{"source_completed_at_ms":-1}}`, err: ErrInvalidInput, calls: []string{"LoadJournalTurn " + testTurn, "HasUnappliedInputs " + testTurn + " 0"}},
	} {
		f := &fakeTx{t: t, loadJournalTurn: func() (JournalTurn, bool, error) { return test.turn, test.found, nil }, hasUnappliedInputs: returns(test.pending)}
		if _, err := turnOperations(t, f).CompleteExecution(t.Context(), testTenant, testSession, testTurn, TurnCompleted, json.RawMessage(test.outcome), "", 0); !errors.Is(err, test.err) {
			t.Fatalf("%s: %v", name, err)
		}
		assertCalls(t, f, test.calls...)
	}
	for name, call := range map[string]func(*ExecutionOperations) error{
		"malformed Turn": func(o *ExecutionOperations) error {
			_, err := o.CompleteExecution(t.Context(), testTenant, testSession, "x", TurnFailed, nil, "", 0)
			return err
		},
		"running status": func(o *ExecutionOperations) error {
			_, err := o.CompleteExecution(t.Context(), testTenant, testSession, testTurn, TurnWaiting, nil, "", 0)
			return err
		},
		"long native": func(o *ExecutionOperations) error {
			_, err := o.CompleteExecution(t.Context(), testTenant, testSession, testTurn, TurnFailed, nil, strings.Repeat("n", 513), 0)
			return err
		},
		"negative applied": func(o *ExecutionOperations) error {
			_, err := o.CompleteExecution(t.Context(), testTenant, testSession, testTurn, TurnFailed, nil, "", -1)
			return err
		},
		"array outcome": func(o *ExecutionOperations) error {
			_, err := o.CompleteExecution(t.Context(), testTenant, testSession, testTurn, TurnFailed, json.RawMessage(`[]`), "", 0)
			return err
		},
	} {
		if err := call(unusedStorage(t)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestBeginTurnArtifactCapture(t *testing.T) {
	f := &fakeTx{t: t, loadTurn: returns(turnWith(TurnInProgress)), hasUnappliedInputs: returns(false), beginArtifactCapture: done}
	if err := turnOperations(t, f).BeginTurnArtifactCapture(t.Context(), testTenant, testSession, testTurn, 3); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadTurn "+testTurn, "HasUnappliedInputs "+testTurn+" 3", "BeginArtifactCapture "+testTurn)

	cancelling := turnWith(TurnInProgress)
	cancelling.CancelRequestedAt = time.Unix(1, 0)
	for _, current := range []Turn{turnWith(TurnWaiting), cancelling} {
		f = &fakeTx{t: t, loadTurn: returns(current)}
		if err := turnOperations(t, f).BeginTurnArtifactCapture(t.Context(), testTenant, testSession, testTurn, 3); !errors.Is(err, ErrTurnConflict) {
			t.Fatal(current.Status, err)
		}
		assertCalls(t, f, "LoadTurn "+testTurn)
	}

	f = &fakeTx{t: t, loadTurn: returns(turnWith(TurnInProgress)), hasUnappliedInputs: returns(true)}
	if err := turnOperations(t, f).BeginTurnArtifactCapture(t.Context(), testTenant, testSession, testTurn, 3); !errors.Is(err, ErrUnappliedInputs) {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadTurn "+testTurn, "HasUnappliedInputs "+testTurn+" 3")

	if err := unusedStorage(t).BeginTurnArtifactCapture(t.Context(), testTenant, testSession, testTurn, -1); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}
