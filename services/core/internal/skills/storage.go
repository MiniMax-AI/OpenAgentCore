package skills

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Storage persists Skill writes. Each method, and each WithVersionDeletion
// callback, runs in one transaction that also records the write's audit.
// Tenant IDs are UUIDs; a malformed one is ErrInvalidInput.
type Storage interface {
	// CreateSkill stores a new Skill with the archive as its version 1, which
	// is both its default and latest version.
	CreateSkill(context.Context, NewSkill) (Skill, error)
	// CreateVersion locks the Skill and stores the archive under the Skill's
	// next version number. It is ErrNotFound for a missing Skill and
	// ErrInvalidInput once the version numbers are exhausted.
	CreateVersion(context.Context, NewVersion) (Version, error)
	// SetDefaultVersion locks the Skill and points its default at an existing
	// version, whose name and description the Skill then takes.
	SetDefaultVersion(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (Skill, error)
	// DeleteSkill deletes the Skill and all its versions.
	DeleteSkill(ctx context.Context, tenantID string, skillID uuid.UUID) error
	// WithVersionDeletion locks the Skill, or is ErrNotFound, and runs apply
	// in that transaction. It commits only when apply returns nil.
	WithVersionDeletion(ctx context.Context, tenantID string, skillID uuid.UUID, apply func(VersionDeletionTx) error) error
}

// NewSkill is a validated upload for a new Skill.
type NewSkill struct {
	TenantID    string
	Name        string
	Description string
	Archive     []byte
}

// NewVersion is a validated upload for an existing Skill. MakeDefault also
// points the Skill's default at the new version.
type NewVersion struct {
	TenantID    string
	SkillID     uuid.UUID
	Name        string
	Description string
	Archive     []byte
	MakeDefault bool
}

// VersionDeletionTx is one locked Skill during a version deletion.
type VersionDeletionTx interface {
	// LoadVersionDeletion loads the facts DecideVersionDeletion needs. It is
	// ErrNotFound when the Skill has no such version.
	LoadVersionDeletion(version int64) (VersionDeletionFacts, error)
	// ApplyVersionDeletion carries out a decided deletion.
	ApplyVersionDeletion(VersionDeletion) error
}

// Reader reads Skills. Reads never lock and never decrypt metadata-only
// results.
type Reader interface {
	Skill(ctx context.Context, tenantID string, id uuid.UUID) (Skill, error)
	// Skills lists one page of the tenant's Skills in creation order, then by
	// ID; a nil After starts at the first Skill.
	Skills(ctx context.Context, tenantID string, page SkillPageQuery) (Page, error)
	Version(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (Version, error)
	// VersionByID finds a version anywhere in the tenant, so a list cursor can
	// tell another Skill's version from a missing one.
	VersionByID(ctx context.Context, tenantID string, id uuid.UUID) (Version, error)
	// Versions lists one page of a Skill's versions in version order.
	Versions(ctx context.Context, tenantID string, skillID uuid.UUID, page VersionPageQuery) (VersionPage, error)
	// VersionContent and DefaultVersionContent decrypt one version's archive.
	VersionContent(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (Content, error)
	DefaultVersionContent(ctx context.Context, tenantID string, skillID uuid.UUID) (Content, error)
}

// SkillPageQuery selects one page of Skills. Limit is at most MaxPageLimit.
type SkillPageQuery struct {
	After     *SkillCursor
	Limit     int
	Ascending bool
}

// SkillCursor is the position of the Skill a page starts after.
type SkillCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// VersionPageQuery selects one page of versions after the version number
// AfterVersion, or from the first when it is 0.
type VersionPageQuery struct {
	AfterVersion int64
	Limit        int
	Ascending    bool
}
