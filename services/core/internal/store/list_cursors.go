package store

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// unresolvedCursor replaces a missing cursor resource with the list's cursor
// error and keeps every other failure.
func unresolvedCursor(err, cursor error) error {
	if errors.Is(err, sessions.ErrNotFound) {
		return cursor
	}
	return err
}
