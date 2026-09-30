package environmenttemplates

import "errors"

var (
	// ErrNotFound reports a Template, or a list cursor, that names no Template of
	// the tenant. Missing, malformed and foreign IDs are indistinguishable.
	ErrNotFound = errors.New("environment template not found")
	// ErrInvalidInput reports input that is not a valid Template, or a saved
	// Template whose configuration no longer decodes as one.
	ErrInvalidInput = errors.New("invalid environment template")
)
