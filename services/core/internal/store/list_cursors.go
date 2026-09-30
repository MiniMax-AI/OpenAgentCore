package store

import "errors"

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
)

// unresolvedCursor replaces a missing cursor resource with the list's cursor
// error and keeps every other failure.
func unresolvedCursor(err, cursor error) error {
	if errors.Is(err, ErrNotFound) {
		return cursor
	}
	return err
}
