package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// event is an execution observation that projects nothing.
func event() ExecutionEvent {
	return ExecutionEvent{Kind: "done", Payload: json.RawMessage(`{}`)}
}

func TestNewJournalBatch(t *testing.T) {
	big := json.RawMessage(`{"text":"` + strings.Repeat("x", 400*1024) + `"}`)
	many := make([]ExecutionEvent, journalBatchEvents+1)
	for i := range many {
		many[i] = event()
	}
	for name, test := range map[string]struct {
		first  int32
		events []ExecutionEvent
		want   error
	}{
		"valid":             {1, []ExecutionEvent{event()}, nil},
		"position zero":     {0, []ExecutionEvent{event()}, ErrInvalidInput},
		"empty":             {1, nil, ErrInvalidInput},
		"too many":          {1, many, ErrInvalidInput},
		"invalid kind":      {1, []ExecutionEvent{{Kind: "Done", Payload: json.RawMessage(`{}`)}}, ErrInvalidInput},
		"not an object":     {1, []ExecutionEvent{{Kind: "done", Payload: json.RawMessage(`[]`)}}, ErrInvalidInput},
		"payload too large": {1, []ExecutionEvent{{Kind: "done", Payload: json.RawMessage(`{"text":"` + strings.Repeat("x", journalPayloadBytes) + `"}`)}}, ErrInvalidInput},
		"batch too large":   {1, []ExecutionEvent{{Kind: "delta", Payload: big}, {Kind: "delta", Payload: big}, {Kind: "delta", Payload: big}}, ErrEventLimit},
	} {
		if _, err := NewJournalBatch(testTurn, test.first, test.events); !errors.Is(err, test.want) || (test.want == nil && err != nil) {
			t.Errorf("%s: %v", name, err)
		}
	}
	batch, err := NewJournalBatch(testTurn, 1, []ExecutionEvent{{Kind: "done", Payload: json.RawMessage(`{ "b": 1, "a": 2 }`)}})
	if err != nil || string(batch.events[0].Payload) != `{"a":2,"b":1}` || batch.bytes != int64(len(`{"a":2,"b":1}`)) {
		t.Fatalf("normalized %s, %d bytes, %v", batch.events[0].Payload, batch.bytes, err)
	}
}

func TestJournalBatchAdmit(t *testing.T) {
	batch, err := NewJournalBatch(testTurn, 3, []ExecutionEvent{event()})
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		turn JournalTurn
		want error
	}{
		"in progress":  {JournalTurn{Status: TurnInProgress, EventCount: 2}, nil},
		"waiting":      {JournalTurn{Status: TurnWaiting, EventCount: 2}, nil},
		"queued":       {JournalTurn{Status: TurnQueued, EventCount: 2}, ErrTurnConflict},
		"ended":        {JournalTurn{Status: TurnCompleted, EventCount: 2}, ErrTurnConflict},
		"gap":          {JournalTurn{Status: TurnInProgress, EventCount: 1}, ErrTurnConflict},
		"byte limit":   {JournalTurn{Status: TurnInProgress, EventCount: 2, EventBytes: journalTurnBytes}, ErrEventLimit},
		"within bytes": {JournalTurn{Status: TurnInProgress, EventCount: 2, EventBytes: journalTurnBytes - batch.bytes}, nil},
	} {
		if err := batch.admit(test.turn); !errors.Is(err, test.want) || (test.want == nil && err != nil) {
			t.Errorf("%s: %v", name, err)
		}
	}
	last, err := NewJournalBatch(testTurn, journalTurnEvents, []ExecutionEvent{event(), event()})
	if err != nil {
		t.Fatal(err)
	}
	if err := last.admit(JournalTurn{Status: TurnInProgress, EventCount: journalTurnEvents - 1}); !errors.Is(err, ErrEventLimit) {
		t.Fatal("journal event limit", err)
	}
}

func TestAppendTurnEvents(t *testing.T) {
	batch, err := NewJournalBatch(testTurn, 2, []ExecutionEvent{event()})
	if err != nil {
		t.Fatal(err)
	}
	running := JournalTurn{Status: TurnInProgress, EventCount: 1}
	found := func(turn JournalTurn) func() (JournalTurn, bool, error) {
		return func() (JournalTurn, bool, error) { return turn, true, nil }
	}
	t.Run("records and projects a new batch", func(t *testing.T) {
		f := &fakeTx{t: t, loadJournalTurn: found(running), insertEvents: done, loadEventSources: returns([]Source{{Turn: testTurn, Kind: "done", Sequence: 2, Payload: json.RawMessage(`{}`)}}), countEvents: done}
		if err := AppendTurnEvents(t.Context(), f, batch); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadJournalTurn "+testTurn, "InsertEvents "+testTurn+" 2 1", "LoadEventSources "+testTurn+" 2", "CountEvents "+testTurn+" 1 2")
	})
	t.Run("replay changes nothing", func(t *testing.T) {
		f := &fakeTx{t: t, loadJournalTurn: found(JournalTurn{Status: TurnCompleted, EventCount: 2}), matchEvents: returns(true)}
		if err := AppendTurnEvents(t.Context(), f, batch); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadJournalTurn "+testTurn, "MatchEvents "+testTurn+" 2 1")
	})
	t.Run("different replay conflicts", func(t *testing.T) {
		f := &fakeTx{t: t, loadJournalTurn: found(JournalTurn{Status: TurnInProgress, EventCount: 2}), matchEvents: returns(false)}
		if err := AppendTurnEvents(t.Context(), f, batch); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal(err)
		}
	})
	t.Run("unadmitted batch writes nothing", func(t *testing.T) {
		f := &fakeTx{t: t, loadJournalTurn: found(JournalTurn{Status: TurnCompleted, EventCount: 1})}
		if err := AppendTurnEvents(t.Context(), f, batch); !errors.Is(err, ErrTurnConflict) {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadJournalTurn "+testTurn)
	})
	t.Run("missing Turn", func(t *testing.T) {
		f := &fakeTx{t: t, loadJournalTurn: func() (JournalTurn, bool, error) { return JournalTurn{}, false, nil }}
		if err := AppendTurnEvents(t.Context(), f, batch); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	})
	t.Run("projection failure stops before counting", func(t *testing.T) {
		f := &fakeTx{t: t, loadJournalTurn: found(running), insertEvents: done, loadEventSources: func() ([]Source, error) { return nil, errStorage }}
		if err := AppendTurnEvents(t.Context(), f, batch); !errors.Is(err, errStorage) {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadJournalTurn "+testTurn, "InsertEvents "+testTurn+" 2 1", "LoadEventSources "+testTurn+" 2")
	})
}

// The terminal outcome goes after the last entry the caller read, whatever the
// journal limits.
func TestAppendTurnEvent(t *testing.T) {
	f := &fakeTx{t: t, insertEvent: done, countEvents: done, loadEventSources: returns([]Source(nil))}
	if err := AppendTurnEvent(t.Context(), f, testTurn, journalTurnEvents, event()); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "InsertEvent "+testTurn+" 65537 done", "CountEvents "+testTurn+" 1 2", "LoadEventSources "+testTurn+" 65537")
}

// ExecutionOperations checks the identifiers, then the batch, then the
// Session: here every Session is missing.
func TestExecutionOperationsValidationOrder(t *testing.T) {
	big := json.RawMessage(`{"text":"` + strings.Repeat("x", 400*1024) + `"}`)
	oversized := []ExecutionEvent{{Kind: "delta", Payload: big}, {Kind: "delta", Payload: big}, {Kind: "delta", Payload: big}}
	operations, err := NewExecutionOperations(&fakeExecutionStorage{t: t, withTurnJournal: func(context.Context, string, string, func(context.Context, TurnJournalTx) error) error {
		return ErrNotFound
	}})
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		tenant, turn string
		first        int32
		events       []ExecutionEvent
		want         error
	}{
		"missing Session":                          {testTenant, testTurn, 1, []ExecutionEvent{event()}, ErrNotFound},
		"invalid batch":                            {testTenant, testTurn, 0, []ExecutionEvent{event()}, ErrInvalidInput},
		"oversized batch":                          {testTenant, testTurn, 1, oversized, ErrEventLimit},
		"malformed Turn with an oversized batch":   {testTenant, "turn", 1, oversized, ErrInvalidInput},
		"malformed Turn in a missing Session":      {testTenant, "turn", 1, []ExecutionEvent{event()}, ErrInvalidInput},
		"malformed tenant with an oversized batch": {"tenant", testTurn, 1, oversized, ErrInvalidInput},
	} {
		if err := operations.AppendTurnEvents(t.Context(), test.tenant, testSession, test.turn, test.first, test.events); !errors.Is(err, test.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
