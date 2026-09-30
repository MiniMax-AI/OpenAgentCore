package sessions

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestDecideCancellation(t *testing.T) {
	requested := turnWith(TurnWaiting)
	requested.CancelRequestedAt = time.Unix(1700000000, 0)
	for name, test := range map[string]struct {
		turn Turn
		want turnCancellation
	}{
		"queued":            {turnWith(TurnQueued), cancellationEndsTurn},
		"waiting":           {turnWith(TurnWaiting), cancellationReportsActivity},
		"waiting requested": {requested, cancellationRequested},
		"in progress":       {turnWith(TurnInProgress), cancellationRequested},
	} {
		if got := decideCancellation(test.turn); got != test.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// CancelTurn decides from the Turn read before the request, and settles with
// the Turn read after it.
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
	t.Run("waiting reports activity", func(t *testing.T) {
		cancelling := turnWith(TurnWaiting)
		cancelling.CancelRequestedAt = time.Unix(1700000001, 0)
		var changes []SessionChange
		f := &fakeTx{t: t, requestTurnCancel: done, loadTurn: returns(cancelling), loadUsage: returns(usage), appendChanges: collect(&changes)}
		if err := CancelTurn(t.Context(), f, turnWith(TurnWaiting)); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "RequestTurnCancel "+testTurn, "LoadTurn "+testTurn, "LoadUsage", "AppendChanges agent.session.in_progress")
		if !reflect.DeepEqual(changes, []SessionChange{ActivityChange(cancelling, usage, nil)}) {
			t.Fatalf("changes %+v", changes)
		}
	})
	t.Run("in progress only requests", func(t *testing.T) {
		f := &fakeTx{t: t, requestTurnCancel: done}
		if err := CancelTurn(t.Context(), f, turnWith(TurnInProgress)); err != nil {
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
