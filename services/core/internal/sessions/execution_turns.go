package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
)

// maxOutcomeBytes bounds a Turn's terminal outcome.
const maxOutcomeBytes = 512 * 1024

// TurnStatusChange is a decided change of a Turn's status.
type TurnStatusChange struct {
	Expected string
	Status   string
	Outcome  json.RawMessage
	// SourceCompletedAt is when the native source completed a Turn that ends
	// completed, zero when it reported none.
	SourceCompletedAt time.Time
}

// TurnTransitionTx is the Session transaction TransitionTurn runs in.
type TurnTransitionTx interface {
	ProjectionTx
	ComputeAdmissionTx
	// LoadTurn reads one of the Session's Turns; a missing one is ErrNotFound.
	LoadTurn(ctx context.Context, turn string) (Turn, error)
	// ApplyTurnStatus moves the Turn as change decides and returns it as
	// stored. The database clock stamps the first start of a Turn that moves
	// to in progress and the end of a Turn that ends, unless the change carries
	// the source completion time. A Turn no longer in the expected status is
	// ErrTurnConflict.
	ApplyTurnStatus(ctx context.Context, turn string, change TurnStatusChange) (Turn, error)
	// LoadEnding reads the facts EndTurn settles an ended Turn with.
	LoadEnding(ctx context.Context, turn string) (Ending, error)
	// ApplyTurnEnd writes what EndTurn decided for an ended Turn.
	ApplyTurnEnd(ctx context.Context, turn string, end TurnEnd) error
}

// TurnTx is the Session transaction of the Turn execution operations.
type TurnTx interface {
	TurnTransitionTx
	TurnEventTx
	TurnJournalTx
	// HasUnappliedInputs reports whether the Turn has message inputs after
	// sequence appliedThrough.
	HasUnappliedInputs(ctx context.Context, turn string, appliedThrough int64) (bool, error)
	// RememberNativeSession records the native session that continues the
	// Session's history on its bound device. A Session without a bound device
	// is ErrNotFound.
	RememberNativeSession(ctx context.Context, native string) error
	// BeginArtifactCapture marks that the Turn captures its Artifacts. A Turn
	// that already does is ErrTurnConflict.
	BeginArtifactCapture(ctx context.Context, turn string) error
}

// TurnExecution is the lease-bound storage of the Turn execution operations.
type TurnExecution interface {
	// WithTurns runs apply in one transaction on the execution lease, under
	// the tenant's Session lock, and commits only when apply succeeds. A
	// malformed ID is ErrInvalidInput and a missing Session ErrNotFound; a
	// publicly deleted Session still journals and settles its Turns.
	WithTurns(ctx context.Context, tenant, session string, apply func(context.Context, TurnTx) error) error
}

// decideTransition decides the status change of current that transition asks
// for. A transition ValidTransition does not allow, an outcome that is not one
// JSON object of at most 512 KiB, and an outcome on a Turn that does not end
// are ErrInvalidInput. A Turn no longer in the expected status, and a Turn
// with a cancellation request that would resume, are ErrTurnConflict.
func decideTransition(current Turn, transition TurnTransition) (TurnStatusChange, error) {
	if !ValidTransition(transition.ExpectedStatus, transition.Status) || len(transition.Outcome) > maxOutcomeBytes {
		return TurnStatusChange{}, fmt.Errorf("%w: invalid turn transition or outcome size", ErrInvalidInput)
	}
	outcome, err := jsonobject.Normalize(transition.Outcome)
	if err != nil {
		return TurnStatusChange{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if !TerminalStatus(transition.Status) && string(outcome) != "{}" {
		return TurnStatusChange{}, fmt.Errorf("%w: outcome requires a terminal status", ErrInvalidInput)
	}
	if current.Status != transition.ExpectedStatus || (transition.Status == TurnInProgress && !current.CancelRequestedAt.IsZero()) {
		return TurnStatusChange{}, ErrTurnConflict
	}
	return TurnStatusChange{Expected: transition.ExpectedStatus, Status: transition.Status, Outcome: outcome}, nil
}

// TransitionTurn moves the Session's root Turn as transition asks, a
// compare-and-set on its status, and publishes what the move settles. A Turn
// that keeps running journals its event. A Turn that ends projects its
// outcome, then settles as EndTurn decides from the Turn read after the
// projection, which may record its usage. An ended Turn is never reopened and
// its outcome never overwritten, including by retries.
func TransitionTurn(ctx context.Context, tx TurnTransitionTx, turn string, transition TurnTransition) (Turn, error) {
	current, err := tx.LoadTurn(ctx, turn)
	if err != nil {
		return Turn{}, err
	}
	change, err := decideTransition(current, transition)
	if err != nil {
		return Turn{}, err
	}
	if change.Expected == TurnQueued && change.Status == TurnInProgress {
		if err := CheckComputeAdmission(ctx, tx); err != nil {
			return Turn{}, err
		}
	}
	moved, err := tx.ApplyTurnStatus(ctx, turn, change)
	if err != nil {
		return Turn{}, err
	}
	if !TerminalStatus(moved.Status) {
		if err := tx.AppendChanges(ctx, TurnChanges(moved, false)...); err != nil {
			return Turn{}, err
		}
		return moved, nil
	}
	if err := ProjectSource(ctx, tx, Source{Turn: moved.ID, Kind: "execution_" + moved.Status, Payload: moved.Outcome, CreatedAt: moved.CompletedAt}); err != nil {
		return Turn{}, err
	}
	return settleEnd(ctx, tx, turn)
}

// settleEnd settles a Turn that ended as EndTurn decides, from the Turn as the
// transaction has left it, and returns that Turn.
func settleEnd(ctx context.Context, tx TurnTransitionTx, turn string) (Turn, error) {
	ended, err := tx.LoadTurn(ctx, turn)
	if err != nil {
		return Turn{}, err
	}
	ending, err := tx.LoadEnding(ctx, turn)
	if err != nil {
		return Turn{}, err
	}
	return ended, tx.ApplyTurnEnd(ctx, turn, EndTurn(ended, ending))
}

// decideCompletion decides whether an execution may end a Turn in status
// current with status: an in-progress Turn ends in any terminal status, and a
// waiting Turn, which still waits on actions, only fails or is cancelled.
// Anything else is ErrTurnConflict.
func decideCompletion(current, status string) error {
	if current == TurnInProgress || (current == TurnWaiting && status != TurnCompleted) {
		return nil
	}
	return ErrTurnConflict
}

// sourceCompletedAt reads the native completion time a completed outcome
// reports in done.source_completed_at_ms, zero when it reports none. An
// outcome that does not decode, or a time that is not positive, is
// ErrInvalidInput. Native and Core timestamps come from independent host
// clocks: the Turn keeps the source time, while committed activity uses the
// database clock.
func sourceCompletedAt(outcome json.RawMessage) (time.Time, error) {
	var snapshot struct {
		Done *struct {
			SourceCompletedAtMS *int64 `json:"source_completed_at_ms"`
		} `json:"done"`
	}
	if json.Unmarshal(outcome, &snapshot) != nil {
		return time.Time{}, ErrInvalidInput
	}
	if snapshot.Done == nil || snapshot.Done.SourceCompletedAtMS == nil {
		return time.Time{}, nil
	}
	ms := *snapshot.Done.SourceCompletedAtMS
	if ms <= 0 {
		return time.Time{}, ErrInvalidInput
	}
	return time.UnixMilli(ms), nil
}

// checkInputsApplied requires that the execution applied every message input
// of the Turn through sequence appliedThrough; otherwise it is
// ErrUnappliedInputs.
func checkInputsApplied(ctx context.Context, tx TurnTx, turn string, appliedThrough int64) error {
	pending, err := tx.HasUnappliedInputs(ctx, turn, appliedThrough)
	if err != nil {
		return err
	}
	if pending {
		return ErrUnappliedInputs
	}
	return nil
}

// TransitionTurn moves the tenant's root Turn as transition asks, as the
// TransitionTurn procedure decides. A dispatcher claims a queued Turn as in
// progress before it sends the Turn's work to a Runtime. A malformed ID is
// ErrInvalidInput.
func (o *ExecutionOperations) TransitionTurn(ctx context.Context, tenant, session, turn string, transition TurnTransition) (Turn, error) {
	if !validID(tenant) || !validID(session) || !validID(turn) {
		return Turn{}, ErrInvalidInput
	}
	var result Turn
	err := o.storage.WithTurns(ctx, tenant, session, func(ctx context.Context, tx TurnTx) error {
		var err error
		result, err = TransitionTurn(ctx, tx, turn, transition)
		return err
	})
	return result, err
}

// CompleteExecution ends the Turn an execution ran with the execution's
// terminal status and outcome. The status change, the outcome's journal entry,
// the native session that continues the Session's history and the Turn's
// settlement, which publishes or discards its staged Artifacts, commit
// together. A Turn completes only when the execution applied its message
// inputs through appliedThrough; otherwise it is ErrUnappliedInputs. A
// malformed ID, a status that does not end the Turn, an outcome that is not
// one JSON object of at most 512 KiB, a native session ID over 512 bytes and a
// negative appliedThrough are ErrInvalidInput.
func (o *ExecutionOperations) CompleteExecution(ctx context.Context, tenant, session, turn, status string, outcome json.RawMessage, nativeID string, appliedThrough int64) (Turn, error) {
	if !validID(tenant) || !validID(session) || !validID(turn) {
		return Turn{}, ErrInvalidInput
	}
	if !TerminalStatus(status) || len(outcome) > maxOutcomeBytes || len(nativeID) > 512 || appliedThrough < 0 {
		return Turn{}, ErrInvalidInput
	}
	outcome, err := jsonobject.Normalize(outcome)
	if err != nil {
		return Turn{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	var result Turn
	err = o.storage.WithTurns(ctx, tenant, session, func(ctx context.Context, tx TurnTx) error {
		current, found, err := tx.LoadJournalTurn(ctx, turn)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if err := decideCompletion(current.Status, status); err != nil {
			return err
		}
		change := TurnStatusChange{Expected: current.Status, Status: status, Outcome: outcome}
		if status == TurnCompleted {
			if err := checkInputsApplied(ctx, tx, turn, appliedThrough); err != nil {
				return err
			}
			if change.SourceCompletedAt, err = sourceCompletedAt(outcome); err != nil {
				return err
			}
		}
		if _, err := tx.ApplyTurnStatus(ctx, turn, change); err != nil {
			return err
		}
		if err := AppendTurnEvent(ctx, tx, turn, current.EventCount, ExecutionEvent{Kind: "execution_" + status, Payload: outcome}); err != nil {
			return err
		}
		if nativeID != "" {
			if err := tx.RememberNativeSession(ctx, nativeID); err != nil {
				return err
			}
		}
		result, err = settleEnd(ctx, tx, turn)
		return err
	})
	return result, err
}

// BeginTurnArtifactCapture admits the capture of an in-progress Turn's
// Artifacts once the execution applied its message inputs through
// appliedThrough; otherwise it is ErrUnappliedInputs. A Turn that is not in
// progress, has a cancellation request or already captures is ErrTurnConflict.
// A malformed ID or a negative appliedThrough is ErrInvalidInput.
func (o *ExecutionOperations) BeginTurnArtifactCapture(ctx context.Context, tenant, session, turn string, appliedThrough int64) error {
	if !validID(tenant) || !validID(session) || !validID(turn) || appliedThrough < 0 {
		return ErrInvalidInput
	}
	return o.storage.WithTurns(ctx, tenant, session, func(ctx context.Context, tx TurnTx) error {
		current, err := tx.LoadTurn(ctx, turn)
		if err != nil {
			return err
		}
		if current.Status != TurnInProgress || !current.CancelRequestedAt.IsZero() {
			return ErrTurnConflict
		}
		if err := checkInputsApplied(ctx, tx, turn, appliedThrough); err != nil {
			return err
		}
		return tx.BeginArtifactCapture(ctx, turn)
	})
}

// AppendTurnEvents records an ordered batch of a Turn's execution observations
// in its journal from position first and projects them, as the
// AppendTurnEvents procedure decides. A malformed tenant, Session or Turn ID is
// ErrInvalidInput, before the batch is validated and before the Session is
// looked up. The journal is Core-internal; it is not the public event stream.
func (o *ExecutionOperations) AppendTurnEvents(ctx context.Context, tenant, session, turn string, first int32, events []ExecutionEvent) error {
	if !validID(tenant) || !validID(session) {
		return ErrInvalidInput
	}
	batch, err := NewJournalBatch(turn, first, events)
	if err != nil {
		return err
	}
	return o.storage.WithTurns(ctx, tenant, session, func(ctx context.Context, tx TurnTx) error {
		return AppendTurnEvents(ctx, tx, batch)
	})
}
