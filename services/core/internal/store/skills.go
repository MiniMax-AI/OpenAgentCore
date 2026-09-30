package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Skill struct {
	ID             string
	Name           string
	Description    string
	CreatedAt      time.Time
	DefaultVersion int64
	LatestVersion  int64
}

type SkillVersion struct {
	ID          string
	SkillID     string
	Version     int64
	Name        string
	Description string
	CreatedAt   time.Time
}

func (s *Store) CreateSkill(ctx context.Context, tenantID string, archive []byte) (Skill, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Skill{}, err
	}
	metadata, err := agentskill.Inspect(archive)
	if err != nil {
		return Skill{}, ErrInvalidInput
	}
	var result Skill
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
		row, err := q.CreateSkill(ctx, sqlc.CreateSkillParams{ID: id, TenantID: tenant, Name: metadata.Name, Description: metadata.Description})
		if err != nil {
			return err
		}
		initial, err := s.saveSkillVersion(ctx, q, tenant, id, 1, metadata, archive)
		if err != nil {
			return err
		}
		result = skillFromRow(row)
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "create", "skill", result.ID, "",
			writeaudit.Resource{Type: "skill", ID: result.ID},
			writeaudit.Resource{Type: "skill_version", ID: initial.ID, ParentID: result.ID})
	})
	return result, err
}

func (s *Store) GetSkill(ctx context.Context, tenantID, skillID string) (Skill, error) {
	tenant, id, err := skillPathIDs(tenantID, skillID)
	if err != nil {
		return Skill{}, err
	}
	row, err := s.queries.GetSkill(ctx, sqlc.GetSkillParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	return skillFromRow(row), err
}

func (s *Store) UpdateSkillDefault(ctx context.Context, tenantID, skillID, version string) (Skill, error) {
	tenant, id, err := skillPathIDs(tenantID, skillID)
	if err != nil {
		return Skill{}, err
	}
	number, err := skills.ParseVersion(version)
	if err != nil {
		return Skill{}, ErrInvalidInput
	}
	var result Skill
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if _, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: id}); err != nil {
			return err
		}
		version, err := q.GetSkillVersion(ctx, sqlc.GetSkillVersionParams{TenantID: tenant, SkillID: id, Version: number})
		if err != nil {
			return err
		}
		row, err := q.SetDefaultSkillVersion(ctx, sqlc.SetDefaultSkillVersionParams{TenantID: tenant, ID: id, DefaultVersion: number, Name: version.Name, Description: version.Description})
		if err != nil {
			return err
		}
		result = skillFromRow(row)
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "update_default_version", "skill", result.ID, "")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return result, err
}

func (s *Store) DeleteSkill(ctx context.Context, tenantID, skillID string) error {
	tenant, id, err := skillPathIDs(tenantID, skillID)
	if err != nil {
		return err
	}
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if _, err := q.DeleteSkill(ctx, sqlc.DeleteSkillParams{TenantID: tenant, ID: id}); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "skill", skillID, "")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) saveSkillVersion(ctx context.Context, q *sqlc.Queries, tenant, skill pgtype.UUID, version int64, metadata agentskill.Metadata, archive []byte) (SkillVersion, error) {
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	body, err := s.credentialCipher.SealSkill(archive, skillBinding(tenant, skill, id, version))
	if err != nil {
		return SkillVersion{}, err
	}
	row, err := q.CreateSkillVersion(ctx, sqlc.CreateSkillVersionParams{ID: id, TenantID: tenant, SkillID: skill, Version: version, Name: metadata.Name, Description: metadata.Description, Contents: body})
	return skillVersionFromRow(sqlc.GetSkillVersionRow(row)), err
}

func skillBinding(tenant, skill, versionID pgtype.UUID, version int64) credentialcrypto.SkillBinding {
	return credentialcrypto.SkillBinding{TenantID: uuid.UUID(tenant.Bytes).String(), SkillID: uuid.UUID(skill.Bytes).String(), VersionID: uuid.UUID(versionID.Bytes).String(), Version: strconv.FormatInt(version, 10)}
}

func skillIDs(tenantID, skillID string) (pgtype.UUID, pgtype.UUID, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return tenant, pgtype.UUID{}, err
	}
	id, err := skillResourceID(skillID, "skill_")
	return tenant, id, err
}

func skillResourceID(value, prefix string) (pgtype.UUID, error) {
	id, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	if err != nil || id == uuid.Nil || value != prefix+id.String() {
		return pgtype.UUID{}, ErrNotFound
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

// skillPathIDs resolves a Skill path identifier. Like pgunit.PathID, a malformed
// value resolves to an identifier that never exists, so the request follows the
// missing-Skill path. Request-body references keep skillIDs.
func skillPathIDs(tenantID, skillID string) (pgtype.UUID, pgtype.UUID, error) {
	tenant, id, err := skillIDs(tenantID, skillID)
	if errors.Is(err, ErrNotFound) {
		return tenant, pgtype.UUID{Bytes: uuid.Max, Valid: true}, nil
	}
	return tenant, id, err
}

// skillPathVersion resolves a version path segment. Versions start at 1, so a
// malformed segment resolves to the never-assigned version 0 and follows the
// missing-version path. Request-body selectors keep skills.ParseVersion.
func skillPathVersion(value string) int64 {
	number, err := skills.ParseVersion(value)
	if err != nil {
		return 0
	}
	return number
}

func skillFromRow(row sqlc.Skill) Skill {
	return Skill{ID: "skill_" + uuid.UUID(row.ID.Bytes).String(), Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt.Time, DefaultVersion: row.DefaultVersion, LatestVersion: row.LatestVersion}
}

func skillVersionFromRow(row sqlc.GetSkillVersionRow) SkillVersion {
	return SkillVersion{ID: "skillver_" + uuid.UUID(row.ID.Bytes).String(), SkillID: "skill_" + uuid.UUID(row.SkillID.Bytes).String(), Version: row.Version, Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt.Time}
}
