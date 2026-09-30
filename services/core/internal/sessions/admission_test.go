package sessions

import (
	"errors"
	"testing"
)

func TestCheckComputeAdmission(t *testing.T) {
	for name, test := range map[string]struct {
		load func() (bool, error)
		want error
	}{
		"running":   {returns(false), nil},
		"suspended": {returns(true), ErrTurnConflict},
		"failing":   {func() (bool, error) { return false, errStorage }, errStorage},
	} {
		f := &fakeTx{t: t, loadComputeSuspension: test.load}
		if err := CheckComputeAdmission(t.Context(), f); !errors.Is(err, test.want) {
			t.Errorf("%s: %v", name, err)
		}
		assertCalls(t, f, "LoadComputeSuspension")
	}
}

// A pending file write is checked before the active Turn.
func TestCheckInputStart(t *testing.T) {
	running := turnWith(TurnInProgress)
	for name, test := range map[string]struct {
		tx    *fakeTx
		want  error
		calls []string
	}{
		"idle":          {&fakeTx{t: t, loadPendingFileWrite: returns(false), loadActiveTurn: activeTurn(nil)}, nil, []string{"LoadPendingFileWrite", "LoadActiveTurn"}},
		"file write":    {&fakeTx{t: t, loadPendingFileWrite: returns(true)}, ErrTurnConflict, []string{"LoadPendingFileWrite"}},
		"active Turn":   {&fakeTx{t: t, loadPendingFileWrite: returns(false), loadActiveTurn: activeTurn(&running)}, ErrTurnConflict, []string{"LoadPendingFileWrite", "LoadActiveTurn"}},
		"failing write": {&fakeTx{t: t, loadPendingFileWrite: func() (bool, error) { return false, errStorage }}, errStorage, []string{"LoadPendingFileWrite"}},
	} {
		if err := CheckInputStart(t.Context(), test.tx); !errors.Is(err, test.want) {
			t.Errorf("%s: %v", name, err)
		}
		assertCalls(t, test.tx, test.calls...)
	}
}
