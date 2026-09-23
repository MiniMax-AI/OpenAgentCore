package store

import (
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/echotext"
)

// InvalidCursorError reports a list `after` cursor that does not name a
// resource of that list once its parents have resolved. Message is the list
// family's observed official message; the API layer selects the family's code
// and param. Missing, malformed, other-type, other-parent and foreign cursors
// all produce the same error, so a cursor never reveals another tenant's
// resources.
type InvalidCursorError struct{ Message string }

func (e *InvalidCursorError) Error() string { return e.Message }

// Official messages for lists that reject an unresolved cursor with 400.
var (
	// Session Items, Subagent Items and Subagent Turn Items.
	errItemCursor = &InvalidCursorError{Message: "Invalid session item ID in `after`"}
	// Subagents and Subagent Turns.
	errResourceCursor = &InvalidCursorError{Message: "Invalid resource ID in `after`"}
	// Session Artifacts.
	errArtifactCursor = &InvalidCursorError{Message: "after is not a valid artifact ID"}
	// A Skill version of another Skill in the same tenant.
	errSkillVersionCursorParent = &InvalidCursorError{Message: "Skill version cursor does not match this skill."}
)

// skillVersionCursorPrefix reports a Skill version cursor that is not a
// version resource ID at all. The official message echoes the value; a long or
// unprintable value is left out so the error stays bounded.
func skillVersionCursorPrefix(value string) error {
	if !echotext.Allowed(value) {
		return &InvalidCursorError{Message: "Invalid 'after'. Expected an ID that begins with 'skillver'."}
	}
	return &InvalidCursorError{Message: fmt.Sprintf("Invalid 'after': '%s'. Expected an ID that begins with 'skillver'.", value)}
}

// unresolvedCursor replaces a missing cursor resource with the list's cursor
// error and keeps every other failure.
func unresolvedCursor(err, cursor error) error {
	if errors.Is(err, ErrNotFound) {
		return cursor
	}
	return err
}

// lookupCursor resolves a cursor of a list whose unresolved cursor is a 404
// (Agents, Sessions, Turns, Templates, Vaults and Credentials) like a path
// identifier: a value that cannot name a resource becomes UnknownResourceID,
// so the list follows exactly the missing-cursor path.
func lookupCursor(value string) string {
	if _, err := parseID(value); err != nil {
		return UnknownResourceID
	}
	return value
}
