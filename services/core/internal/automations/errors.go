package automations

import "errors"

// ErrNotFound reports an Automation, an invocation, or a list cursor that
// names none of the tenant's. Missing, malformed and foreign IDs are
// indistinguishable.
var ErrNotFound = errors.New("automation not found")

// InvalidError reports a request value that breaks a rule, with the member
// that holds it.
type InvalidError struct{ Param, Message string }

func (e *InvalidError) Error() string { return e.Message }

// ConflictError reports an operation the Automation's state does not allow.
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }
