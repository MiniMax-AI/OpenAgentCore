package agents

import "errors"

var (
	// ErrNotFound reports an Agent the tenant does not have, including an
	// identifier that cannot name one.
	ErrNotFound = errors.New("agent not found")
	// ErrInvalidInput reports configuration, metadata or a request limit that
	// the saved Agent rules reject.
	ErrInvalidInput = errors.New("invalid agent input")
)
