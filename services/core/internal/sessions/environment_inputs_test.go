package sessions

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

const testReservation = "6a1c9e2b-3d4f-4a5b-8c6d-7e8f9a0b1c2d"

// reservations is a fake LoadInputReservation that reads states one call at
// a time, then the last again.
func reservations(states ...EnvironmentInputReservation) func() (EnvironmentInputReservation, error) {
	return func() (EnvironmentInputReservation, error) {
		state := states[0]
		if len(states) > 1 {
			states = states[1:]
		}
		return state, nil
	}
}

func reservationIn(state string) EnvironmentInputReservation {
	return EnvironmentInputReservation{ID: testReservation, SessionID: testSession, Key: inputKey, State: state, Inputs: []Input{messageInput("hi")}}
}

func TestCheckEnvironmentAcceptsInput(t *testing.T) {
	hosted := json.RawMessage(`{"type":"openai_hosted"}`)
	for _, test := range []struct {
		environment Environment
		want        error
	}{
		{selfHosted, nil},
		{Environment{Status: "failed", Configuration: hosted}, ErrHostedEnvironmentFailed},
		{Environment{Status: "failed", Configuration: selfHosted.Configuration}, ErrEnvironmentUnavailable},
		{Environment{Status: "expired", Configuration: hosted}, ErrEnvironmentUnavailable},
	} {
		if err := checkEnvironmentAcceptsInput(test.environment); err != test.want {
			t.Errorf("%s %s: %v", test.environment.Status, test.environment.Configuration, err)
		}
	}
}

func TestJoinsActiveTurn(t *testing.T) {
	capturing := turnWith(TurnInProgress)
	capturing.ArtifactCaptureStarted = true
	if joinsActiveTurn(Turn{}, false) || !joinsActiveTurn(turnWith(TurnInProgress), true) || joinsActiveTurn(capturing, true) {
		t.Fatal("joins")
	}
}

func TestReserveEnvironmentInput(t *testing.T) {
	const batch = `[{"kind":"message","payload":{"text":"hi"}}]`
	find := "FindInputReservation request " + batch
	reserve := func(t *testing.T, tx *fakeInputTx) (EnvironmentInputReservation, error) {
		tx.loadEnvironmentInput = inputs()
		return deviceService(t, &fakeStorage{t: t, inputTx: tx}).ReserveEnvironmentInput(t.Context(), testTenant, testSession, inputKey, []Input{messageInput("hi")})
	}
	// fresh is a transaction where the key holds no batch yet.
	fresh := func(t *testing.T) *fakeInputTx {
		tx := newInputTx(t)
		tx.findInputReservation = func() (*EnvironmentInputReservation, bool, error) { return nil, false, nil }
		tx.loadInputBatch = func() ([]InputReceipt, bool, error) { return nil, true, nil }
		tx.loadPendingFileWrite, tx.loadEnvironment = returns(false), returns(selfHosted)
		return tx
	}
	checked := []string{"LoadEnvironmentInput", find, "LoadInputBatch request " + batch, "LoadPendingFileWrite", "LoadEnvironment"}
	t.Run("a retry returns its reservation", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			current EnvironmentInputReservation
			audit   []string
		}{
			{"pending", reservationIn(EnvironmentInputPending), []string{"RecordInputAudit"}},
			{"expired at its deadline", reservationIn(EnvironmentInputExpired), nil},
		} {
			t.Run(test.name, func(t *testing.T) {
				previous := reservationIn(EnvironmentInputPending)
				tx := newInputTx(t)
				tx.findInputReservation = func() (*EnvironmentInputReservation, bool, error) { return &previous, true, nil }
				tx.expireInputReservation, tx.loadInputReservation, tx.recordInputAudit = done, returns(test.current), done
				result, err := reserve(t, tx)
				if err != nil || !reflect.DeepEqual(result, test.current) {
					t.Fatalf("reservation %+v, %v", result, err)
				}
				calls := append([]string{"LoadEnvironmentInput", find, "ExpireInputReservation " + testReservation, "LoadInputReservation " + testReservation}, test.audit...)
				assertCalls(t, tx.fakeTx, append(calls, "LoadEnvironmentInput")...)
			})
		}
	})
	t.Run("a retry of a direct admission returns its receipts", func(t *testing.T) {
		previous := []InputReceipt{{Sequence: 3, TurnID: testTurn, Replayed: true}}
		tx := newInputTx(t)
		tx.findInputReservation = func() (*EnvironmentInputReservation, bool, error) { return nil, false, nil }
		tx.loadInputBatch = func() ([]InputReceipt, bool, error) { return previous, true, nil }
		tx.recordInputAudit = done
		result, err := reserve(t, tx)
		if err != nil || !reflect.DeepEqual(result, EnvironmentInputReservation{SessionID: testSession, State: EnvironmentInputAdmitted, Receipts: previous}) {
			t.Fatalf("reservation %+v, %v", result, err)
		}
		assertCalls(t, tx.fakeTx, "LoadEnvironmentInput", find, "LoadInputBatch request "+batch, "RecordInputAudit", "LoadEnvironmentInput")
	})
	t.Run("an Environment that no longer runs rejects input", func(t *testing.T) {
		for _, test := range []struct {
			name string
			load func() (Environment, error)
			want error
		}{
			{"failed hosted", returns(Environment{Status: "failed", Configuration: json.RawMessage(`{"type":"openai_hosted"}`)}), ErrHostedEnvironmentFailed},
			{"expired", returns(Environment{Status: "expired", Configuration: selfHosted.Configuration}), ErrEnvironmentUnavailable},
			{"missing", func() (Environment, error) { return Environment{}, ErrNotFound }, ErrInvalidInput},
		} {
			t.Run(test.name, func(t *testing.T) {
				tx := fresh(t)
				tx.loadEnvironment = test.load
				if _, err := reserve(t, tx); err != test.want {
					t.Fatal(err)
				}
				assertCalls(t, tx.fakeTx, checked...)
			})
		}
	})
	t.Run("input joins the active Turn", func(t *testing.T) {
		running := turnWith(TurnInProgress)
		tx := fresh(t)
		tx.loadInputGate, tx.loadActiveTurn, tx.createTurnInput, tx.loadInputSource, tx.recordInputAudit = returns(InputGate{Matches: true}), activeTurn(&running), sequences(4), unprojected, done
		result, err := reserve(t, tx)
		if err != nil || !reflect.DeepEqual(result, EnvironmentInputReservation{SessionID: testSession, State: EnvironmentInputAdmitted, Receipts: []InputReceipt{{Sequence: 4, TurnID: testTurn}}}) {
			t.Fatalf("reservation %+v, %v", result, err)
		}
		assertCalls(t, tx.fakeTx, append(checked, "LoadInputGate request "+batch, "LoadActiveTurn",
			"LoadActiveTurn", "CreateTurnInput "+testTurn+" request 0 message", "LoadInputSource 4", "RecordInputAudit", "LoadEnvironmentInput")...)
	})
	t.Run("input waits in a reservation on an idle Session", func(t *testing.T) {
		tx := fresh(t)
		tx.loadInputGate, tx.loadActiveTurn = returns(InputGate{Matches: true}), activeTurn(nil)
		tx.createInputReservation, tx.recordInputAudit = returns(reservationIn(EnvironmentInputPending)), done
		result, err := reserve(t, tx)
		if err != nil || !reflect.DeepEqual(result, reservationIn(EnvironmentInputPending)) {
			t.Fatalf("reservation %+v, %v", result, err)
		}
		assertCalls(t, tx.fakeTx, append(checked, "LoadInputGate request "+batch, "LoadActiveTurn", "CreateInputReservation request "+batch, "RecordInputAudit", "LoadEnvironmentInput")...)
	})
	t.Run("a cancellation touches no storage", func(t *testing.T) {
		if _, err := deviceService(t, &fakeStorage{t: t}).ReserveEnvironmentInput(t.Context(), testTenant, testSession, inputKey, []Input{cancelInput}); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	})
}

func TestExpireEnvironmentInput(t *testing.T) {
	tx := newInputTx(t)
	tx.loadEnvironmentInput, tx.expireInputReservation = inputs(), done
	tx.loadInputReservation = reservations(reservationIn(EnvironmentInputPending), reservationIn(EnvironmentInputExpired))
	service := deviceService(t, &fakeStorage{t: t, inputTx: tx})
	result, err := service.ExpireEnvironmentInput(t.Context(), testTenant, testSession, testReservation)
	if err != nil || result.State != EnvironmentInputExpired {
		t.Fatalf("reservation %+v, %v", result, err)
	}
	assertCalls(t, tx.fakeTx, "LoadEnvironmentInput", "LoadInputReservation "+testReservation, "ExpireInputReservation "+testReservation, "LoadInputReservation "+testReservation, "LoadEnvironmentInput")
	if _, err := service.ExpireEnvironmentInput(t.Context(), testTenant, testSession, "malformed"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}
