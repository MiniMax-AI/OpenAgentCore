package sessions

import "context"

// TurnCancellationTx is the Session transaction CancelTurn runs in.
type TurnCancellationTx interface {
	JournalTx
	// RequestTurnCancel records a cancellation request on an active Turn of
	// the Session. A queued Turn becomes cancelled; the database clock stamps
	// the request and the end.
	RequestTurnCancel(ctx context.Context, turn string) error
	// LoadTurn reads one of the Session's Turns.
	LoadTurn(ctx context.Context, turn string) (Turn, error)
	// LoadEnding reads the facts EndTurn settles an ended Turn with.
	LoadEnding(ctx context.Context, turn string) (Ending, error)
	// ApplyTurnEnd writes what EndTurn decided for an ended Turn.
	ApplyTurnEnd(ctx context.Context, turn string, end TurnEnd) error
}

// CancelTurn requests the cancellation of turn, the Session's active Turn read
// under the Session lock. A queued Turn never started and ends cancelled, read
// again after the request, which set its timestamps. Any other Turn ends when
// its execution stops, and a waiting Turn keeps its Session activity until then.
func CancelTurn(ctx context.Context, tx TurnCancellationTx, turn Turn) error {
	if err := tx.RequestTurnCancel(ctx, turn.ID); err != nil {
		return err
	}
	if turn.Status != TurnQueued {
		return nil
	}
	cancelled, err := tx.LoadTurn(ctx, turn.ID)
	if err != nil {
		return err
	}
	ending, err := tx.LoadEnding(ctx, turn.ID)
	if err != nil {
		return err
	}
	return tx.ApplyTurnEnd(ctx, turn.ID, EndTurn(cancelled, ending))
}

// CancellationTx is the Session transaction CancelWork runs in.
type CancellationTx interface {
	TurnCancellationTx
	ActiveTurnTx
	// CancelPendingInput settles the Session's pending Environment input
	// reservation as cancelled.
	CancelPendingInput(ctx context.Context) error
}

// CancelWork cancels the Session's active work: it requests the cancellation
// of the active Turn, then cancels the pending Environment input. Cancellation
// requests do not prove that native work has stopped.
func CancelWork(ctx context.Context, tx CancellationTx) error {
	turn, active, err := tx.LoadActiveTurn(ctx)
	if err != nil {
		return err
	}
	if active {
		if err := CancelTurn(ctx, tx, turn); err != nil {
			return err
		}
	}
	return tx.CancelPendingInput(ctx)
}
