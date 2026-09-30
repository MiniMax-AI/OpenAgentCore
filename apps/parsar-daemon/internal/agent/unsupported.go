package agent

import "errors"

// ErrUnsupportedOperation marks an intentional absence of optional behavior.
// Implementations wrap it with a static reason and perform no native effects.
// It is distinct from unavailable resources, failures and uncertain outcomes.
var ErrUnsupportedOperation = errors.New("agent: unsupported operation")
