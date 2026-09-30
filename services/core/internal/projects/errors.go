package projects

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound reports a missing Project, key or list cursor, including a
	// malformed identifier.
	ErrNotFound = errors.New("project or API key not found")
	// ErrInvalidInput reports a malformed identifier for a new Project or key,
	// or a list page size outside 1..MaxListLimit.
	ErrInvalidInput = errors.New("invalid project input")
	// ErrArchived reports an operation that needs an active Project.
	ErrArchived = errors.New("project is archived")
	// ErrExists reports a Project ID that is already in use.
	ErrExists = errors.New("project ID already exists")
	// ErrAPIKeyExists reports an API key ID that is already in use.
	ErrAPIKeyExists = errors.New("API key ID already exists")
)

// NameError rejects a Project or key display name.
type NameError struct {
	// MaxLength is the name's limit in Unicode code points.
	MaxLength int
}

func (e *NameError) Error() string {
	return fmt.Sprintf("name must contain 1–%d characters without controls", e.MaxLength)
}
