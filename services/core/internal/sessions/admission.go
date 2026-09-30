package sessions

import "context"

// ComputeAdmissionTx reads the Session's managed compute for
// CheckComputeAdmission.
type ComputeAdmissionTx interface {
	// LoadComputeSuspension reports whether the Session's managed compute is in
	// its suspension cycle: quiescing, suspending, suspended, restoring or
	// waking.
	LoadComputeSuspension(ctx context.Context) (bool, error)
}

// CheckComputeAdmission admits a new execution owner, such as a Turn that
// starts or a file write, only while the Session's managed compute is running
// or has no suspension; otherwise it is ErrTurnConflict. Existing receipts stay
// readable in every phase.
func CheckComputeAdmission(ctx context.Context, tx ComputeAdmissionTx) error {
	suspended, err := tx.LoadComputeSuspension(ctx)
	if err != nil {
		return err
	}
	if suspended {
		return ErrTurnConflict
	}
	return nil
}

// FileWriteGateTx reads the Session's Environment file writes for
// CheckFileWriteGate.
type FileWriteGateTx interface {
	// LoadPendingFileWrite reports whether a file write to the Session's
	// Environment is pending.
	LoadPendingFileWrite(ctx context.Context) (bool, error)
}

// CheckFileWriteGate holds new work while a file write to the Session's
// Environment is pending: it is ErrTurnConflict.
func CheckFileWriteGate(ctx context.Context, tx FileWriteGateTx) error {
	pending, err := tx.LoadPendingFileWrite(ctx)
	if err != nil {
		return err
	}
	if pending {
		return ErrTurnConflict
	}
	return nil
}

// InputStartTx is the Session transaction CheckInputStart runs in.
type InputStartTx interface {
	FileWriteGateTx
	ActiveTurnTx
}

// CheckInputStart lets reserved Environment input, or a file write, start only
// on an idle Session: a pending file write, checked first, and an active Turn
// are both ErrTurnConflict.
func CheckInputStart(ctx context.Context, tx InputStartTx) error {
	if err := CheckFileWriteGate(ctx, tx); err != nil {
		return err
	}
	_, active, err := tx.LoadActiveTurn(ctx)
	if err != nil {
		return err
	}
	if active {
		return ErrTurnConflict
	}
	return nil
}
