package sessions

import "context"

// InputExecution is the lease-bound storage of Environment input settlement.
type InputExecution interface {
	// WithInputs runs apply in one transaction on the execution lease, under
	// the lock of the tenant's Session, and commits only when apply succeeds.
	// A malformed tenant is ErrInvalidInput; a malformed, missing or deleted
	// Session is ErrNotFound.
	WithInputs(ctx context.Context, tenant, session string, apply func(context.Context, InputTx) error) error
	// WithDueInputReservations runs apply in one transaction on the execution
	// lease for each of at most 32 pending Environment input reservations
	// whose deadline has passed, earliest deadline first, of live Sessions
	// whose lock no other transaction holds, with that Session's transaction
	// and the reservation's ID. It commits only when every apply succeeds.
	WithDueInputReservations(ctx context.Context, apply func(context.Context, InputTx, string) error) error
}

// PromoteEnvironmentInput admits a pending reservation's batch into a new Turn
// and claims that Turn as in progress for the execution owner's retained
// native preparation. The reservation settles as admitted with the batch's
// receipts. A reservation that already settled, or expires now because its
// deadline has passed, is returned as it is. The Session must be idle with no
// pending file write; otherwise it is ErrTurnConflict and the reservation
// stays pending. A malformed reservation ID is ErrInvalidInput, and a missing
// reservation ErrNotFound.
func (o *ExecutionOperations) PromoteEnvironmentInput(ctx context.Context, tenant, session, reservation string) (EnvironmentInputReservation, error) {
	if !validID(reservation) {
		return EnvironmentInputReservation{}, ErrInvalidInput
	}
	var result EnvironmentInputReservation
	err := o.storage.WithInputs(ctx, tenant, session, func(ctx context.Context, tx InputTx) error {
		return TrackInputActivity(ctx, tx, func(ctx context.Context) error {
			current, err := tx.LoadInputReservation(ctx, reservation)
			if err != nil {
				return err
			}
			if result, err = expireReservation(ctx, tx, current); err != nil || result.State != EnvironmentInputPending {
				return err
			}
			if err := CheckInputStart(ctx, tx); err != nil {
				return err
			}
			for position, input := range result.Inputs {
				receipt, err := AdmitInput(ctx, tx, result.Key, int32(position), input)
				if err != nil {
					return err
				}
				result.Receipts = append(result.Receipts, receipt)
			}
			settled, err := tx.AdmitInputReservation(ctx, reservation)
			if err != nil {
				return err
			}
			start := TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress}
			if _, err := TransitionTurn(ctx, tx, result.Receipts[0].TurnID, start); err != nil {
				return err
			}
			result.State, result.SettledAt = EnvironmentInputAdmitted, &settled
			return nil
		})
	})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	return result, nil
}

// FailEnvironmentInput settles a pending reservation as failed before
// admission with code, model_provider_required or runtime_preparation_failed,
// once execution confirmed the failure. A reservation that already settled,
// including one cancelled meanwhile, keeps its outcome, and one whose deadline
// has passed expires instead. Any other code and a malformed reservation ID
// are ErrInvalidInput.
func (o *ExecutionOperations) FailEnvironmentInput(ctx context.Context, tenant, session, reservation, code string) error {
	if code != "model_provider_required" && code != "runtime_preparation_failed" {
		return ErrInvalidInput
	}
	if !validID(reservation) {
		return ErrInvalidInput
	}
	return o.storage.WithInputs(ctx, tenant, session, func(ctx context.Context, tx InputTx) error {
		return TrackInputActivity(ctx, tx, func(ctx context.Context) error {
			if err := tx.ExpireInputReservation(ctx, reservation); err != nil {
				return err
			}
			return tx.FailInputReservation(ctx, reservation, code)
		})
	})
}

// ExpireEnvironmentInputs expires one bounded batch of pending reservations
// whose deadline has passed, reports the input activity each changes, creates
// no Turn history and returns how many it expired. It is cross-Session
// maintenance only the execution owner runs.
func (o *ExecutionOperations) ExpireEnvironmentInputs(ctx context.Context) (int64, error) {
	var expired int64
	err := o.storage.WithDueInputReservations(ctx, func(ctx context.Context, tx InputTx, reservation string) error {
		if err := TrackInputActivity(ctx, tx, func(ctx context.Context) error { return tx.ExpireInputReservation(ctx, reservation) }); err != nil {
			return err
		}
		expired++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}
