package sessions

import "context"

// FunctionCallMatch is what a Turn's stored call shows about a reported call.
type FunctionCallMatch struct {
	// Recorded reports that the Turn already has a call with the public
	// identity.
	Recorded bool
	// Matches reports that the recorded call has the same executor identity,
	// name and arguments.
	Matches bool
}

// FunctionTx is one Turn's function calls inside a Session transaction on the
// execution lease.
type FunctionTx interface {
	JournalTx
	PendingFunctionCallsTx
	// MatchFunctionCall compares call with the Turn's call of the same public
	// identity.
	MatchFunctionCall(ctx context.Context, turn string, call FunctionCall) (FunctionCallMatch, error)
	// CreateFunctionCall records a new call of the Turn. A call the Turn
	// already has is ErrIdempotencyConflict.
	CreateFunctionCall(ctx context.Context, turn string, call FunctionCall) error
	// LoadFunctionCall reads the Turn's call and reports whether it has it.
	LoadFunctionCall(ctx context.Context, turn, call string) (FunctionCall, bool, error)
	// ApplyFunctionResult records the application receipt of the call's
	// result.
	ApplyFunctionResult(ctx context.Context, turn, call string) error
	// ApplyFunctionTurnStatus moves the Turn from expected to status and
	// returns it as stored. A Turn no longer in expected is ErrTurnConflict.
	ApplyFunctionTurnStatus(ctx context.Context, turn, expected, status string) (Turn, error)
}

// FunctionExecution is the lease-bound storage of function calls.
type FunctionExecution interface {
	// WithFunctionTurn runs apply in one transaction on the execution lease,
	// under the Session lock, with the tenant's Turn as the transaction reads
	// it. It commits only when apply succeeds. A malformed ID is
	// ErrInvalidInput, and a missing Session or Turn is ErrNotFound; a publicly
	// deleted Session still settles its Turn.
	WithFunctionTurn(ctx context.Context, tenant, session, turn string, apply func(context.Context, FunctionTx, Turn) error) error
}

// decideFunctionCall decides whether to record a call an executor reported:
// a retry of the recorded call records nothing, a different call with the same
// public identity conflicts, and a new call needs a Turn that accepts results.
func decideFunctionCall(turn Turn, match FunctionCallMatch) (bool, error) {
	if match.Recorded {
		if !match.Matches {
			return false, ErrIdempotencyConflict
		}
		return false, nil
	}
	if !acceptsFunctionResults(turn) {
		return false, ErrTurnConflict
	}
	return true, nil
}

// decideFunctionConfirmation decides whether to record the native application
// of a call's result: an applied call records nothing, and a call without a
// result or on a Turn that no longer accepts results conflicts.
func decideFunctionConfirmation(turn Turn, call FunctionCall) (bool, error) {
	if call.Applied {
		return false, nil
	}
	if len(call.Result) == 0 || !acceptsFunctionResults(turn) {
		return false, ErrTurnConflict
	}
	return true, nil
}

// RecordFunctionCall records a call an executor reported for its Turn and
// journals the Turn's required actions with it. A retry of the recorded call
// changes nothing.
func (o *ExecutionOperations) RecordFunctionCall(ctx context.Context, tenant, session, turn string, call FunctionCall) error {
	if err := validateFunctionCall(call); err != nil {
		return err
	}
	return o.storage.WithFunctionTurn(ctx, tenant, session, turn, func(ctx context.Context, tx FunctionTx, current Turn) error {
		match, err := tx.MatchFunctionCall(ctx, current.ID, call)
		if err != nil {
			return err
		}
		record, err := decideFunctionCall(current, match)
		if err != nil || !record {
			return err
		}
		if err := tx.CreateFunctionCall(ctx, current.ID, call); err != nil {
			return err
		}
		return settleFunctionState(ctx, tx, current)
	})
}

// PendingFunctionCalls returns the Turn's calls without an application
// receipt, with their saved results. A cancelling or ended Turn has none.
func (o *ExecutionOperations) PendingFunctionCalls(ctx context.Context, tenant, session, turn string) ([]FunctionCall, error) {
	calls := make([]FunctionCall, 0)
	err := o.storage.WithFunctionTurn(ctx, tenant, session, turn, func(ctx context.Context, tx FunctionTx, current Turn) error {
		pending, err := tx.LoadPendingFunctionCalls(ctx, current.ID)
		calls = append(calls, pending...)
		return err
	})
	if err != nil {
		return nil, err
	}
	return calls, nil
}

// ConfirmFunctionResult records that the executor applied the call's saved
// result, not that the tool succeeded, and journals the Turn's remaining
// required actions. A repeated receipt changes nothing.
func (o *ExecutionOperations) ConfirmFunctionResult(ctx context.Context, tenant, session, turn, call string) error {
	if !validFunctionIdentity(call) {
		return ErrInvalidInput
	}
	return o.storage.WithFunctionTurn(ctx, tenant, session, turn, func(ctx context.Context, tx FunctionTx, current Turn) error {
		stored, found, err := tx.LoadFunctionCall(ctx, current.ID, call)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		apply, err := decideFunctionConfirmation(current, stored)
		if err != nil || !apply {
			return err
		}
		if err := tx.ApplyFunctionResult(ctx, current.ID, call); err != nil {
			return err
		}
		return settleFunctionState(ctx, tx, current)
	})
}

// settleFunctionState moves a Turn that accepts results to waiting while it
// has required actions and back to in progress once it has none, journals
// that transition, then journals the Session activity with the required
// actions.
func settleFunctionState(ctx context.Context, tx FunctionTx, turn Turn) error {
	actions, err := LoadRequiredActions(ctx, tx, turn)
	if err != nil {
		return err
	}
	status := TurnInProgress
	if len(actions) > 0 {
		status = TurnWaiting
	}
	if turn.Status != status {
		if turn, err = tx.ApplyFunctionTurnStatus(ctx, turn.ID, turn.Status, status); err != nil {
			return err
		}
		if err := tx.AppendChanges(ctx, TurnChanges(turn, false)...); err != nil {
			return err
		}
	}
	usage, err := tx.LoadUsage(ctx)
	if err != nil {
		return err
	}
	return tx.AppendChanges(ctx, ActivityChange(turn, usage, actions))
}
