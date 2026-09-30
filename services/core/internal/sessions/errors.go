package sessions

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidInput           = errors.New("invalid session input")
	ErrEnvironmentUnavailable = errors.New("environment is no longer available")
	ErrNotFound               = errors.New("session not found")
	ErrIdempotencyConflict    = errors.New("idempotency key was already used with different input")
)

var ErrTurnConflict = errors.New("turn state changed or cancellation was requested")

// ErrInputPending rejects a new input batch while earlier Session input
// still waits for admission. It remains a Turn conflict for internal callers.
var ErrInputPending = fmt.Errorf("%w: session input is still pending", ErrTurnConflict)

// ErrHostedEnvironmentFailed rejects new input on a Session whose hosted
// Environment failed to provision. It remains ErrEnvironmentUnavailable for
// internal callers; an expired Environment keeps that plain error.
var ErrHostedEnvironmentFailed = fmt.Errorf("%w: the hosted environment failed to provision", ErrEnvironmentUnavailable)

// ErrNotIdle rejects deletion of a Session that still has work or input
// pending. Callers cancel first and delete after the Session settles.
var ErrNotIdle = errors.New("session must be durably idle or failed without required actions before deletion")

var ErrEventLimit = errors.New("execution event storage limit exceeded")

var ErrUnappliedInputs = errors.New("turn has messages without an executor receipt")

var ErrStreamGap = errors.New("live event buffer exceeded; recover through Session and Items reads")

var ErrDeviceBindingConflict = errors.New("session is already bound to a different device")

var ErrInstallationAuthorization = errors.New("installation authorization is invalid or expired; obtain a new command from the Session")

var ErrExecutorCredentialExists = errors.New("executor key ID already exists; rotate explicitly")

// Result targets are resolved only inside a tenant-owned Session, after its
// lookup, so a missing or foreign Session still returns ErrNotFound (EVT-11).
var (
	// ErrUnknownFunctionCall rejects a result whose call_id names no function
	// call in the Session.
	ErrUnknownFunctionCall = errors.New("unknown pending tool call")
	// ErrFunctionCallTurnMismatch rejects a result whose call exists in the
	// Session but not in the named Turn, including a malformed or unknown Turn.
	ErrFunctionCallTurnMismatch = errors.New("tool call belongs to a different turn")
)

// ErrFunctionResultConflict rejects a result that differs from the one already
// saved for its call, including after the Turn ended (EVT-12).
var ErrFunctionResultConflict = errors.New("tool call already has a different result")

// CursorError reports a list `after` cursor that does not name a
// resource of that list once its parents have resolved. Message is the list
// family's observed official message; the API layer selects the family's code
// and param. Missing, malformed, other-type, other-parent and foreign cursors
// all produce the same error, so a cursor never reveals another tenant's
// resources.
type CursorError struct{ Message string }

func (e *CursorError) Error() string { return e.Message }

// Official messages for lists that reject an unresolved cursor with 400.
var (
	// Session Items, Subagent Items and Subagent Turn Items.
	ErrItemCursor = &CursorError{Message: "Invalid session item ID in `after`"}
	// Subagents and Subagent Turns.
	ErrResourceCursor = &CursorError{Message: "Invalid resource ID in `after`"}
	// Session Artifacts.
	ErrArtifactCursor = &CursorError{Message: "after is not a valid artifact ID"}
)
