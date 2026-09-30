package skills

import (
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/echotext"
)

var (
	// ErrNotFound reports a Skill or version that does not exist in the tenant.
	// Malformed identifiers and other tenants' resources are indistinguishable
	// from missing ones.
	ErrNotFound = errors.New("skill not found")
	// ErrInvalidInput reports an invalid archive, version, page limit or tenant.
	ErrInvalidInput = errors.New("invalid skill request")
	// ErrDefaultVersion rejects deleting the default version while other
	// versions remain.
	ErrDefaultVersion = errors.New("cannot delete the default skill version")
)

// CursorError reports a version list cursor that does not name a version of
// the listed Skill. Message is the official message for the case.
type CursorError struct{ Message string }

func (e *CursorError) Error() string { return e.Message }

// errCursorParent is a version of another Skill in the same tenant.
var errCursorParent = &CursorError{Message: "Skill version cursor does not match this skill."}

// cursorPrefixError reports a cursor that is not a version ID at all. The
// official message echoes the value; a long or unprintable value is left out
// so the error stays bounded.
func cursorPrefixError(value string) error {
	if !echotext.Allowed(value) {
		return &CursorError{Message: "Invalid 'after'. Expected an ID that begins with 'skillver'."}
	}
	return &CursorError{Message: fmt.Sprintf("Invalid 'after': '%s'. Expected an ID that begins with 'skillver'.", value)}
}
