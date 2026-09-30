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

// Reader reads a tenant's Files.
type Reader interface {
	// Get returns the File's metadata, or ErrNotFound.
	Get(ctx context.Context, tenantID, fileID string) (File, error)
	// List returns one page. An After cursor that names no File of the tenant
	// returns ErrNotFound.
	List(ctx context.Context, tenantID string, query ListQuery) (Page, error)
	// Read passes consume the File's metadata and content from one snapshot, so
	// a concurrent Delete never truncates a read it admitted. It returns
	// consume's error unchanged.
	Read(ctx context.Context, tenantID, fileID string, consume func(File, io.Reader) error) error
}
