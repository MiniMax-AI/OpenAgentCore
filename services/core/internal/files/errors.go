package files

import "errors"

var (
	// ErrNotFound reports a File the tenant does not own, including a malformed
	// or foreign ID, so missing and foreign Files are indistinguishable.
	ErrNotFound = errors.New("file not found")
	// ErrInvalidInput reports an upload envelope, list query or tenant that
	// Core does not accept.
	ErrInvalidInput = errors.New("invalid file request")
	// ErrTooLarge reports content beyond the operation's limit.
	ErrTooLarge = errors.New("file exceeds the content limit")
)
