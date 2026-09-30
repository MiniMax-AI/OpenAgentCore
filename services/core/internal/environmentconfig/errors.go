package environmentconfig

import "errors"

// ErrInvalid reports configuration that breaks a rule of this package.
var ErrInvalid = errors.New("invalid environment configuration")
