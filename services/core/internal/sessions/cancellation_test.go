package sessions

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// CancelTurn ends a queued Turn with the Turn read after the request, and only
// records the request for a Turn that started.
func TestCancelTurn(t *testing.T) {
	usage := json.RawMessage(`{"input_tokens":1}`)
	t.Run("queued ends cancelled", func(t *testing.T) {
		cancelled, ending := turnWith(TurnCancelled), Ending{Usage: usage}
		var ends []TurnEnd
		f := &fakeTx{t: t, requestTurnCancel: done, loadTurn: returns(cancelled), loadEnding: returns(ending),
			applyTurnEnd: func(end TurnEnd) error { ends = append(ends, end); return nil }}
		if err := CancelTurn(t.Context(), f, turnWith(TurnQueued)); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "RequestTurnCancel "+testTurn, "LoadTurn "+testTurn, "LoadEnding "+testTurn, "ApplyTurnEnd "+testTurn)
		if !reflect.DeepEqual(ends, []TurnEnd{EndTurn(cancelled, ending)}) {
			t.Fatalf("end %+v", ends)
		}
	})
	t.Run("waiting only requests", func(t *testing.T) {
		f := &fakeTx{t: t, requestTurnCancel: done}
		if err := CancelTurn(t.Context(), f, turnWith(TurnWaiting)); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "RequestTurnCancel "+testTurn)
	})
}

func TestCancelWork(t *testing.T) {
	idle := &fakeTx{t: t, loadActiveTurn: activeTurn(nil), cancelPendingInput: done}
	if err := CancelWork(t.Context(), idle); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, idle, "LoadActiveTurn", "CancelPendingInput")

	running := turnWith(TurnInProgress)
	active := &fakeTx{t: t, loadActiveTurn: activeTurn(&running), requestTurnCancel: done, cancelPendingInput: done}
	if err := CancelWork(t.Context(), active); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, active, "LoadActiveTurn", "RequestTurnCancel "+testTurn, "CancelPendingInput")

	failing := &fakeTx{t: t, loadActiveTurn: activeTurn(&running), requestTurnCancel: func() error { return errStorage }}
	if err := CancelWork(t.Context(), failing); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	assertCalls(t, failing, "LoadActiveTurn", "RequestTurnCancel "+testTurn)
}
