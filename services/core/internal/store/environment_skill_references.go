package store

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// freezeEnvironmentSkills runs only for a newly inserted Session, in its transaction.
// Resource locks serialize selection with pointer changes, version deletion and
// resource deletion. The returned copy no longer depends on any source resource.
func (s *Store) freezeEnvironmentSkills(ctx context.Context, q *sqlc.Queries, tenantID string, setup EnvironmentSetup) (EnvironmentSetup, error) {
	if err := setup.Validate(); err != nil {
		return EnvironmentSetup{}, err
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
		tenant, skill, err := skillIDs(tenantID, id)
		if err != nil {
			return EnvironmentSetup{}, err
		}
		owner, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: skill})
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		if err != nil {
			return EnvironmentSetup{}, err
		}
		owners[id] = owner
	}
	result := setup
	result.Skills = append([]EnvironmentSkill(nil), setup.Skills...)
	for i, skill := range result.Skills {
		if skill.Metadata.Type != "skill_reference" {
			continue
		}
		owner := owners[skill.Metadata.SkillID]
		number := owner.DefaultVersion
		switch skill.Metadata.Version {
		case "":
		case "latest":
			number = owner.LatestVersion
		default:
			var err error
			number, err = skillVersionNumber(skill.Metadata.Version)
			if err != nil {
				return EnvironmentSetup{}, err
			}
		}
		row, err := q.ReadSkillVersion(ctx, sqlc.ReadSkillVersionParams{TenantID: owner.TenantID, SkillID: owner.ID, Version: number})
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		if err != nil {
			return EnvironmentSetup{}, err
		}
		version, archive, err := s.openSkillVersion(row)
		if err != nil {
			return EnvironmentSetup{}, err
		}
		result.Skills[i] = EnvironmentSkill{Metadata: EnvironmentSkillMetadata{Type: "skill_reference", SkillID: version.SkillID, Version: strconv.FormatInt(version.Version, 10), Name: version.Name, Description: version.Description}, Archive: archive}
	}
	return result, result.validate(true)
}
