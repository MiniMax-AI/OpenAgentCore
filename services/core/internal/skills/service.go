package skills

import (
	"context"
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/google/uuid"
)

// MaxPageLimit bounds every Skill and version page. A limit of 0 returns an
// empty page whose HasMore reports whether anything follows the cursor.
const MaxPageLimit = 100

// Service runs the Skill use cases.
type Service struct {
	storage Storage
	reader  Reader
}

func NewService(storage Storage, reader Reader) (*Service, error) {
	if storage == nil || reader == nil {
		return nil, errors.New("skills: storage and reader are required")
	}
	return &Service{storage: storage, reader: reader}, nil
}

// CreateSkill uploads a new Skill.
type CreateSkill struct {
	TenantID string
	Archive  []byte
}

// CreateSkill validates the archive and stores it as the new Skill's
// version 1.
func (s *Service) CreateSkill(ctx context.Context, c CreateSkill) (Skill, error) {
	metadata, err := agentskill.Inspect(c.Archive)
	if err != nil {
		return Skill{}, ErrInvalidInput
	}
	return s.storage.CreateSkill(ctx, NewSkill{TenantID: c.TenantID, Name: metadata.Name, Description: metadata.Description, Archive: c.Archive})
}

// CreateVersion uploads a new version of a Skill.
type CreateVersion struct {
	TenantID    string
	SkillID     uuid.UUID
	Archive     []byte
	MakeDefault bool
}

// CreateVersion validates the archive before looking up the Skill, then
// stores it under the Skill's next version number.
func (s *Service) CreateVersion(ctx context.Context, c CreateVersion) (Version, error) {
	metadata, err := agentskill.Inspect(c.Archive)
	if err != nil {
		return Version{}, ErrInvalidInput
	}
	return s.storage.CreateVersion(ctx, NewVersion{TenantID: c.TenantID, SkillID: c.SkillID, Name: metadata.Name, Description: metadata.Description, Archive: c.Archive, MakeDefault: c.MakeDefault})
}

// SetDefaultVersion points a Skill's default at one of its versions. Version
// is the request's selector and must be a concrete version number.
type SetDefaultVersion struct {
	TenantID string
	SkillID  uuid.UUID
	Version  string
}

func (s *Service) SetDefaultVersion(ctx context.Context, c SetDefaultVersion) (Skill, error) {
	number, err := ParseVersion(c.Version)
	if err != nil {
		return Skill{}, ErrInvalidInput
	}
	return s.storage.SetDefaultVersion(ctx, c.TenantID, c.SkillID, number)
}

// DeleteSkill deletes a Skill and its versions. Session installations frozen
// from them are independent copies and remain.
type DeleteSkill struct {
	TenantID string
	SkillID  uuid.UUID
}

func (s *Service) DeleteSkill(ctx context.Context, c DeleteSkill) error {
	return s.storage.DeleteSkill(ctx, c.TenantID, c.SkillID)
}

// DeleteVersion deletes one version of a Skill.
type DeleteVersion struct {
	TenantID string
	SkillID  uuid.UUID
	Version  int64
}

// DeleteVersion decides under the Skill's lock, so it serializes with uploads
// and pointer changes, and returns the deleted version.
func (s *Service) DeleteVersion(ctx context.Context, c DeleteVersion) (Version, error) {
	var deleted Version
	err := s.storage.WithVersionDeletion(ctx, c.TenantID, c.SkillID, func(tx VersionDeletionTx) error {
		facts, err := tx.LoadVersionDeletion(c.Version)
		if err != nil {
			return err
		}
		decision, err := DecideVersionDeletion(facts)
		if err != nil {
			return err
		}
		if err := tx.ApplyVersionDeletion(decision); err != nil {
			return err
		}
		deleted = decision.Target
		return nil
	})
	if err != nil {
		return Version{}, err
	}
	return deleted, nil
}

// ListSkills lists the tenant's Skills. After is a Skill ID; a malformed or
// missing one is ErrNotFound.
type ListSkills struct {
	TenantID  string
	After     string
	Limit     int
	Ascending bool
}

func (s *Service) ListSkills(ctx context.Context, c ListSkills) (Page, error) {
	if c.Limit < 0 || c.Limit > MaxPageLimit {
		return Page{}, ErrInvalidInput
	}
	query := SkillPageQuery{Limit: c.Limit, Ascending: c.Ascending}
	if c.After != "" {
		id := PathID(c.After)
		cursor, err := s.reader.Skill(ctx, c.TenantID, id)
		if err != nil {
			return Page{}, err
		}
		query.After = &SkillCursor{CreatedAt: cursor.CreatedAt, ID: id}
	}
	return s.reader.Skills(ctx, c.TenantID, query)
}

// ListVersions lists a Skill's versions. After is a version ID, not a
// version number.
type ListVersions struct {
	TenantID  string
	SkillID   uuid.UUID
	After     string
	Limit     int
	Ascending bool
}

// ListVersions resolves the Skill before the cursor. A cursor that is not a
// version ID is a CursorError; a missing version, including another
// tenant's, is ErrNotFound; another Skill's version in the tenant is a
// CursorError. The Skill is looked up again before that last error, so a
// Skill deleted in between stays not found.
func (s *Service) ListVersions(ctx context.Context, c ListVersions) (VersionPage, error) {
	if c.Limit < 0 || c.Limit > MaxPageLimit {
		return VersionPage{}, ErrInvalidInput
	}
	if _, err := s.reader.Skill(ctx, c.TenantID, c.SkillID); err != nil {
		return VersionPage{}, err
	}
	query := VersionPageQuery{Limit: c.Limit, Ascending: c.Ascending}
	if c.After != "" {
		if !strings.HasPrefix(c.After, "skillver") {
			return VersionPage{}, cursorPrefixError(c.After)
		}
		id, err := ParseVersionID(c.After)
		if err != nil {
			return VersionPage{}, err
		}
		cursor, err := s.reader.VersionByID(ctx, c.TenantID, id)
		if err != nil {
			return VersionPage{}, err
		}
		if cursor.SkillID != FormatID(c.SkillID) {
			if _, err := s.reader.Skill(ctx, c.TenantID, c.SkillID); err != nil {
				return VersionPage{}, err
			}
			return VersionPage{}, errCursorParent
		}
		query.AfterVersion = cursor.Version
	}
	return s.reader.Versions(ctx, c.TenantID, c.SkillID, query)
}

// ReadVersion reads one version's archive.
type ReadVersion struct {
	TenantID string
	SkillID  uuid.UUID
	Version  int64
}

func (s *Service) ReadVersion(ctx context.Context, c ReadVersion) (Content, error) {
	return verified(s.reader.VersionContent(ctx, c.TenantID, c.SkillID, c.Version))
}

// ReadDefaultVersion reads the archive of a Skill's default version, selecting
// the pointer and the content in one read.
type ReadDefaultVersion struct {
	TenantID string
	SkillID  uuid.UUID
}

func (s *Service) ReadDefaultVersion(ctx context.Context, c ReadDefaultVersion) (Content, error) {
	return verified(s.reader.DefaultVersionContent(ctx, c.TenantID, c.SkillID))
}

func verified(content Content, err error) (Content, error) {
	if err == nil {
		err = VerifyContent(content)
	}
	if err != nil {
		return Content{}, err
	}
	return content, nil
}
