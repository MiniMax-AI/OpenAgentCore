package sessions

import (
	"context"
	"errors"
)

// checkEnvironmentAcceptsInput admits new input only while the Session's
// Environment is live: a failed hosted Environment is
// ErrHostedEnvironmentFailed, and any other failed or expired Environment
// ErrEnvironmentUnavailable.
func checkEnvironmentAcceptsInput(environment Environment) error {
	if environment.Status == "failed" {
		if kind, err := EnvironmentType(environment.Configuration); err == nil && kind == "openai_hosted" {
			return ErrHostedEnvironmentFailed
		}
	}
	if environment.Status == "failed" || environment.Status == "expired" {
		return ErrEnvironmentUnavailable
	}
	return nil
}

// joinsActiveTurn reports whether reserved Environment input joins the
// Session's active Turn at once instead of waiting for its own Turn: it does
// while the Turn has not started capturing its Artifacts, whose sealed native
// input takes nothing more.
func joinsActiveTurn(turn Turn, active bool) bool {
	return active && !turn.ArtifactCaptureStarted
}

// ReserveEnvironmentInput admits a message batch for a Session whose
// Environment runs it. Under the Session lock it joins the active Turn at once,
// unless that Turn captures its Artifacts, or otherwise reserves the batch
// until a deadline, for the execution owner to promote into a new Turn. A
// retry returns the reservation under its key, expired once its deadline has
// passed, or the receipts the batch was admitted with. The checks of
// SubmitInputs apply, and a failed or expired Environment rejects new input.
func (s *Service) ReserveEnvironmentInput(ctx context.Context, tenant, session, key string, inputs []Input) (EnvironmentInputReservation, error) {
	if err := ValidateInputKey(key); err != nil {
		return EnvironmentInputReservation{}, err
	}
	batch, encoded, err := ValidateMessageInputs(inputs)
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	var result EnvironmentInputReservation
	err = s.storage.WithInputs(ctx, tenant, session, func(ctx context.Context, tx InputTx) error {
		return TrackInputActivity(ctx, tx, func(ctx context.Context) error {
			previous, matches, err := tx.FindInputReservation(ctx, key, encoded)
			if err != nil {
				return err
			}
			replay, err := decideReplay(previous != nil, matches)
			if err != nil {
				return err
			}
			if replay {
				if result, err = expireReservation(ctx, tx, *previous); err != nil {
					return err
				}
				if result.State == EnvironmentInputPending || result.State == EnvironmentInputAdmitted {
					return tx.RecordInputAudit(ctx)
				}
				return nil
			}
			receipts, matches, err := tx.LoadInputBatch(ctx, key, encoded)
			if err != nil {
				return err
			}
			if replay, err = decideReplay(len(receipts) > 0, matches); err != nil {
				return err
			}
			if replay {
				// Earlier direct admission has receipts, but never had a reservation or deadline.
				result = EnvironmentInputReservation{SessionID: session, State: EnvironmentInputAdmitted, Receipts: receipts}
				return tx.RecordInputAudit(ctx)
			}
			if err := CheckFileWriteGate(ctx, tx); err != nil {
				return err
			}
			environment, err := tx.LoadEnvironment(ctx)
			if errors.Is(err, ErrNotFound) {
				return ErrInvalidInput
			}
			if err != nil {
				return err
			}
			if err := checkEnvironmentAcceptsInput(environment); err != nil {
				return err
			}
			gate, err := tx.LoadInputGate(ctx, key, encoded)
			if err != nil {
				return err
			}
			if err := checkInputGate(gate); err != nil {
				return err
			}
			turn, active, err := tx.LoadActiveTurn(ctx)
			if err != nil {
				return err
			}
			if joinsActiveTurn(turn, active) {
				result = EnvironmentInputReservation{SessionID: session, State: EnvironmentInputAdmitted}
				for position, input := range batch {
					receipt, err := AdmitInput(ctx, tx, key, int32(position), input)
					if err != nil {
						return err
					}
					result.Receipts = append(result.Receipts, receipt)
				}
				return tx.RecordInputAudit(ctx)
			}
			if result, err = tx.CreateInputReservation(ctx, key, encoded); err != nil {
				return err
			}
			return tx.RecordInputAudit(ctx)
		})
	})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	return result, nil
}

// ExpireEnvironmentInput returns the Session's Environment input reservation,
// expired first when it is pending and its deadline has passed by the
// database clock. A malformed reservation ID is ErrInvalidInput, and a missing
// reservation ErrNotFound.
func (s *Service) ExpireEnvironmentInput(ctx context.Context, tenant, session, reservation string) (EnvironmentInputReservation, error) {
	if !validID(reservation) {
		return EnvironmentInputReservation{}, ErrInvalidInput
	}
	var result EnvironmentInputReservation
	err := s.storage.WithInputs(ctx, tenant, session, func(ctx context.Context, tx InputTx) error {
		return TrackInputActivity(ctx, tx, func(ctx context.Context) error {
			current, err := tx.LoadInputReservation(ctx, reservation)
			if err != nil {
				return err
			}
			result, err = expireReservation(ctx, tx, current)
			return err
		})
	})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	return result, nil
}

// expireReservation expires a pending reservation whose deadline has passed
// and returns it as the transaction has left it. A settled one is returned as
// it is: its outcome is a successful result, so settling never rolls back.
func expireReservation(ctx context.Context, tx InputTx, reservation EnvironmentInputReservation) (EnvironmentInputReservation, error) {
	if reservation.State != EnvironmentInputPending {
		return reservation, nil
	}
	if err := tx.ExpireInputReservation(ctx, reservation.ID); err != nil {
		return EnvironmentInputReservation{}, err
	}
	return tx.LoadInputReservation(ctx, reservation.ID)
}
