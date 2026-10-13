package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func (f *fakeExecutionStorage) WithInputs(ctx context.Context, tenant, session string, apply func(context.Context, InputTx) error) error {
	f.t.Helper()
	if f.withInputs == nil {
		f.t.Fatal("unexpected call to WithInputs")
	}
	return f.withInputs(ctx, tenant, session, apply)
}

func (f *fakeExecutionStorage) WithDueInputReservations(ctx context.Context, apply func(context.Context, InputTx, string) error) error {
	f.t.Helper()
	if f.withDueInputReservations == nil {
		f.t.Fatal("unexpected call to WithDueInputReservations")
	}
	return f.withDueInputReservations(ctx, apply)
}

// inputOperations serves the tenant's Session through tx.
func inputOperations(t *testing.T, tx *fakeInputTx) *ExecutionOperations {
	t.Helper()
	tx.loadEnvironmentInput = inputs()
	operations, err := NewExecutionOperations(&fakeExecutionStorage{t: t, withInputs: func(ctx context.Context, tenant, session string, apply func(context.Context, InputTx) error) error {
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

func TestPromoteEnvironmentInput(t *testing.T) {
	expire := []string{"LoadEnvironmentInput", "LoadInputReservation " + testReservation, "ExpireInputReservation " + testReservation, "LoadInputReservation " + testReservation}
	promote := func(t *testing.T, tx *fakeInputTx) (EnvironmentInputReservation, error) {
		return inputOperations(t, tx).PromoteEnvironmentInput(t.Context(), testTenant, testSession, testReservation)
	}
	t.Run("a reservation past its deadline expires", func(t *testing.T) {
		tx := newInputTx(t)
		tx.loadInputReservation, tx.expireInputReservation = reservations(reservationIn(EnvironmentInputPending), reservationIn(EnvironmentInputExpired)), done
		result, err := promote(t, tx)
		if err != nil || !reflect.DeepEqual(result, reservationIn(EnvironmentInputExpired)) {
			t.Fatalf("reservation %+v, %v", result, err)
		}
		assertCalls(t, tx.fakeTx, append(expire, "LoadEnvironmentInput")...)
	})
	t.Run("a pending reservation starts its Turn", func(t *testing.T) {
		settled := time.Unix(1700000002, 0)
		tx := newInputTx(t)
		tx.loadInputReservation, tx.expireInputReservation = returns(reservationIn(EnvironmentInputPending)), done
		tx.loadPendingFileWrite, tx.loadActiveTurn = returns(false), activeTurn(nil)
		tx.createTurn, tx.createTurnInput, tx.loadInputSource = returns(turnWith(TurnQueued)), sequences(5), unprojected
		tx.loadUsage, tx.appendChanges = returns(json.RawMessage(`{}`)), collect(new([]SessionChange))
		tx.admitInputReservation, tx.loadTurn, tx.loadComputeSuspension = returns(settled), returns(turnWith(TurnQueued)), returns(false)
		tx.applyTurnStatus = func(TurnStatusChange) (Turn, error) { return turnWith(TurnInProgress), nil }
		result, err := promote(t, tx)
		want := reservationIn(EnvironmentInputAdmitted)
		want.SettledAt, want.Receipts = &settled, []InputReceipt{{Sequence: 5, TurnID: testTurn}}
		if err != nil || !reflect.DeepEqual(result, want) {
			t.Fatalf("reservation %+v, %v", result, err)
		}
		assertCalls(t, tx.fakeTx, append(expire, "LoadPendingFileWrite", "LoadActiveTurn",
			"LoadActiveTurn", "CreateTurn", "AppendChanges agent.session.turn.created", "CreateTurnInput "+testTurn+" request 0 message", "LoadInputSource 5", "LoadUsage", "AppendChanges agent.session.in_progress",
			"AdmitInputReservation "+testReservation,
			"LoadTurn "+testTurn, "LoadComputeSuspension", "ApplyTurnStatus "+testTurn+" queued in_progress {}", "AppendChanges agent.session.turn.in_progress",
			"LoadEnvironmentInput")...)
	})
	t.Run("a busy Session keeps the reservation pending", func(t *testing.T) {
		running := turnWith(TurnInProgress)
		tx := newInputTx(t)
		tx.loadInputReservation, tx.expireInputReservation = returns(reservationIn(EnvironmentInputPending)), done
		tx.loadPendingFileWrite, tx.loadActiveTurn = returns(false), activeTurn(&running)
		if _, err := promote(t, tx); !errors.Is(err, ErrTurnConflict) {
			t.Fatal(err)
		}
		assertCalls(t, tx.fakeTx, append(expire, "LoadPendingFileWrite", "LoadActiveTurn")...)
	})
	if _, err := unusedStorage(t).PromoteEnvironmentInput(t.Context(), testTenant, testSession, "malformed"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestFailEnvironmentInput(t *testing.T) {
	tx := newInputTx(t)
	tx.expireInputReservation, tx.failInputReservation = done, done
	if err := inputOperations(t, tx).FailEnvironmentInput(t.Context(), testTenant, testSession, testReservation, "runtime_preparation_failed"); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, tx.fakeTx, "LoadEnvironmentInput", "ExpireInputReservation "+testReservation, "FailInputReservation "+testReservation+" runtime_preparation_failed", "LoadEnvironmentInput")
	for reservation, code := range map[string]string{testReservation: "other", "malformed": "model_provider_required"} {
		if err := unusedStorage(t).FailEnvironmentInput(t.Context(), testTenant, testSession, reservation, code); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s %s: %v", reservation, code, err)
		}
	}
}

func TestExpireEnvironmentInputs(t *testing.T) {
	due := []string{"6a1c9e2b-0000-4a5b-8c6d-7e8f9a0b1c2d", "6a1c9e2b-1111-4a5b-8c6d-7e8f9a0b1c2d"}
	expire := func(t *testing.T, tx *fakeInputTx, failure error) (int64, error) {
		tx.loadEnvironmentInput, tx.expireInputReservation = inputs(), done
		operations, err := NewExecutionOperations(&fakeExecutionStorage{t: t, withDueInputReservations: func(ctx context.Context, apply func(context.Context, InputTx, string) error) error {
			for _, reservation := range due {
				if err := apply(ctx, tx, reservation); err != nil {
					return err
				}
			}
			return failure
		}})
		if err != nil {
			t.Fatal(err)
		}
		return operations.ExpireEnvironmentInputs(t.Context())
	}
	tx := newInputTx(t)
	if expired, err := expire(t, tx, nil); err != nil || expired != 2 {
		t.Fatalf("expired %d, %v", expired, err)
	}
	assertCalls(t, tx.fakeTx, "LoadEnvironmentInput", "ExpireInputReservation "+due[0], "LoadEnvironmentInput", "LoadEnvironmentInput", "ExpireInputReservation "+due[1], "LoadEnvironmentInput")
	if expired, err := expire(t, newInputTx(t), errStorage); !errors.Is(err, errStorage) || expired != 0 {
		t.Fatalf("expired %d, %v", expired, err)
	}
}
