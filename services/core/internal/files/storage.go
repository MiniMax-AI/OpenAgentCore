package files

import (
	"context"
	"io"
)

// Storage persists Files. Each method runs in one transaction.
type Storage interface {
	// Create allocates the content, calls write with a writer for it, and stores
	// the File with the returned envelope only when write returns nil. A
	// storage failure while writing takes precedence over write's own error.
	Create(ctx context.Context, tenantID string, write func(io.Writer) (Upload, error)) (File, error)
	// Delete removes the File and its content, or returns ErrNotFound.
	Delete(ctx context.Context, tenantID, fileID string) error
}
