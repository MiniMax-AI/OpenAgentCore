package projects

import (
	"context"
	"crypto/sha256"
)

// Storage is the persistence the Project use cases write through. Each method
// runs in one transaction and records its administrator audit row in that
// transaction, from the source in the context; a missing or malformed source
// aborts the write.
type Storage interface {
	// CreateProject stores the Project with a new tenant and its execution
	// scope. An ID already in use returns ErrExists; a malformed ID returns
	// ErrInvalidInput.
	CreateProject(ctx context.Context, project NewProject) (Project, error)
	// RenameProject returns ErrNotFound for a missing Project.
	RenameProject(ctx context.Context, id, name string) (Project, error)
	// ArchiveProject archives the Project and revokes all of its keys. Both are
	// idempotent. It returns ErrNotFound for a missing Project.
	ArchiveProject(ctx context.Context, id string) (Project, error)
	// WithKeyIssuance runs issue while it holds a shared lock on the Project, so
	// the Project cannot be archived until the new key commits. It returns
	// ErrNotFound for a missing Project without calling issue.
	WithKeyIssuance(ctx context.Context, projectID string, issue func(KeyIssuanceTx) error) error
	// RevokeAPIKey revokes one key of the Project, idempotently. It returns
	// ErrNotFound for a missing Project or a key of another Project.
	RevokeAPIKey(ctx context.Context, projectID, keyID string) error
}

// KeyIssuanceTx is one key issuance transaction.
type KeyIssuanceTx interface {
	// LoadProject returns the state of the locked Project.
	LoadProject(ctx context.Context) (LockedProject, error)
	// ApplyAPIKey stores the key. An ID already in use returns
	// ErrAPIKeyExists; a malformed ID returns ErrInvalidInput.
	ApplyAPIKey(ctx context.Context, key NewAPIKey) (APIKey, error)
}

// NewProject is a Project to store. The adapter allocates its tenant.
type NewProject struct {
	ID, Name string
	// OrganizationID and ExternalProjectID form the execution scope the
	// Project's keys authenticate as, and SubjectID is their subject.
	OrganizationID, ExternalProjectID, SubjectID string
}

// LockedProject is the state a key issuance decides on.
type LockedProject struct {
	Archived bool
}

// NewAPIKey is a key to store: its display prefix and secret digest, never
// the secret.
type NewAPIKey struct {
	ID, Name, Prefix string
	Digest           [sha256.Size]byte
}

// Reader answers Project and key queries.
type Reader interface {
	// GetProject returns ErrNotFound for a missing Project or malformed ID.
	GetProject(ctx context.Context, id string) (Binding, error)
	// ListProjects returns ErrNotFound when query.After names no Project.
	ListProjects(ctx context.Context, query ListQuery) (Page, error)
	// ListAPIKeys returns every key of the Project, including revoked keys. It
	// returns ErrNotFound for a missing Project or when query.After names no key
	// of this Project.
	ListAPIKeys(ctx context.Context, projectID string, query ListQuery) (KeyPage, error)
	// ResolveAPIKey returns the active key with this secret digest, in an
	// active Project, or ErrNotFound.
	ResolveAPIKey(ctx context.Context, digest [sha256.Size]byte) (KeyBinding, error)
	// APIKeyDigestExists reports whether any key, including a revoked key or a
	// key of an archived Project, has this secret digest.
	APIKeyDigestExists(ctx context.Context, digest [sha256.Size]byte) (bool, error)
}
