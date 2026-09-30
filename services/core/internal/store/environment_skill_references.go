package store

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// freezeEnvironmentSkills runs only for a newly inserted Session, in its transaction.
// Resource locks serialize selection with pointer changes, version deletion and
// resource deletion. The returned copy no longer depends on any source resource.
func (s *Store) freezeEnvironmentSkills(ctx context.Context, q *sqlc.Queries, tenantID string, setup environmentconfig.Setup) (environmentconfig.Setup, error) {
	if setup.Validate() != nil {
		return environmentconfig.Setup{}, sessions.ErrInvalidInput
	}
	owners := make(map[string]sqlc.Skill)
	for _, skill := range setup.Skills {
		if skill.Metadata.Type == "skill_reference" {
			owners[skill.Metadata.SkillID] = sqlc.Skill{}
		}
	}
	// Opposite caller list orders must not produce opposite database lock orders.
	ids := make([]string, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		tenant, err := parseID(tenantID)
		if err != nil {
			return environmentconfig.Setup{}, err
		}
		skill, err := skills.ParseID(id)
		if err != nil {
			return environmentconfig.Setup{}, sessions.ErrNotFound
		}
		owner, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: pgtype.UUID{Bytes: skill, Valid: true}})
		if errors.Is(err, pgx.ErrNoRows) {
			err = sessions.ErrNotFound
		}
		if err != nil {
			return environmentconfig.Setup{}, err
		}
		owners[id] = owner
	}
	result := setup
	result.Skills = append([]environmentconfig.Skill(nil), setup.Skills...)
	for i, skill := range result.Skills {
		if skill.Metadata.Type != "skill_reference" {
			continue
		}
		owner := owners[skill.Metadata.SkillID]
		number, err := skills.SelectVersion(skill.Metadata.Version, owner.DefaultVersion, owner.LatestVersion)
		if err != nil {
			return environmentconfig.Setup{}, sessions.ErrInvalidInput
		}
		row, err := q.ReadSkillVersion(ctx, sqlc.ReadSkillVersionParams{TenantID: owner.TenantID, SkillID: owner.ID, Version: number})
		if errors.Is(err, pgx.ErrNoRows) {
			err = sessions.ErrNotFound
		}
		if err != nil {
			return environmentconfig.Setup{}, err
		}
		content, err := s.openFrozenSkill(row)
		if err != nil {
			return environmentconfig.Setup{}, err
		}
		version := content.Version
		result.Skills[i] = environmentconfig.Skill{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: version.SkillID, Version: strconv.FormatInt(version.Version, 10), Name: version.Name, Description: version.Description}, Archive: content.Archive}
	}
	if result.ValidateInstalled() != nil {
		return environmentconfig.Setup{}, sessions.ErrInvalidInput
	}
	return result, nil
}

// openFrozenSkill decrypts a version the Session transaction selected and
// checks it is still the bundle its version records.
func (s *Store) openFrozenSkill(row sqlc.SkillVersion) (skills.Content, error) {
	archive, err := s.credentialCipher.OpenSkill(row.Contents, credentialcrypto.NewSkillBinding(row.TenantID.Bytes, row.SkillID.Bytes, row.ID.Bytes, row.Version))
	if err != nil {
		return skills.Content{}, err
	}
	content := skills.Content{Version: skills.Version{ID: skills.FormatVersionID(row.ID.Bytes), SkillID: skills.FormatID(row.SkillID.Bytes), Version: row.Version, Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt.Time}, Archive: archive}
	if skills.VerifyContent(content) != nil {
		return skills.Content{}, sessions.ErrInvalidInput
	}
	return content, nil
}
