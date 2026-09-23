package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type SkillPage struct {
	Skills  []Skill
	HasMore bool
}

type SkillVersionPage struct {
	Versions []SkillVersion
	HasMore  bool
}

// ListSkills and ListSkillVersions accept limit 0: the page is empty and HasMore
// reports whether any resource follows the cursor.
func (s *Store) ListSkills(ctx context.Context, tenantID, after string, limit int, ascending bool) (SkillPage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SkillPage{}, err
	}
	if limit < 0 || limit > 100 {
		return SkillPage{}, ErrInvalidInput
	}
	params := sqlc.ListSkillsParams{TenantID: tenant, PageLimit: int32(limit + 1), Ascending: ascending, AfterID: pgtype.UUID{Valid: true}}
	if after != "" {
		cursor, err := s.GetSkill(ctx, tenantID, after)
		if err != nil {
			return SkillPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
		params.AfterID, _ = skillResourceID(cursor.ID, "skill_")
	}
	rows, err := s.queries.ListSkills(ctx, params)
	if err != nil {
		return SkillPage{}, err
	}
	page := SkillPage{Skills: make([]Skill, 0, min(limit, len(rows))), HasMore: len(rows) > limit}
	if page.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Skills = append(page.Skills, skillFromRow(row))
	}
	return page, nil
}

func (s *Store) ListSkillVersions(ctx context.Context, tenantID, skillID, after string, limit int, ascending bool) (SkillVersionPage, error) {
	tenant, id, err := skillPathIDs(tenantID, skillID)
	if err != nil {
		return SkillVersionPage{}, err
	}
	if limit < 0 || limit > 100 {
		return SkillVersionPage{}, ErrInvalidInput
	}
	if _, err := s.GetSkill(ctx, tenantID, skillID); err != nil {
		return SkillVersionPage{}, err
	}
	params := sqlc.ListSkillVersionsParams{TenantID: tenant, SkillID: id, PageLimit: int32(limit + 1), Ascending: ascending}
	if after != "" {
		cursorID, err := skillResourceID(after, "skillver_")
		if err != nil {
			return SkillVersionPage{}, err
		}
		cursor, err := s.queries.GetSkillVersionByID(ctx, sqlc.GetSkillVersionByIDParams{TenantID: tenant, SkillID: id, ID: cursorID})
		if errors.Is(err, pgx.ErrNoRows) {
			return SkillVersionPage{}, ErrNotFound
		}
		if err != nil {
			return SkillVersionPage{}, err
		}
		params.AfterVersion = pgtype.Int8{Int64: cursor.Version, Valid: true}
	}
	rows, err := s.queries.ListSkillVersions(ctx, params)
	if err != nil {
		return SkillVersionPage{}, err
	}
	page := SkillVersionPage{Versions: make([]SkillVersion, 0, min(limit, len(rows))), HasMore: len(rows) > limit}
	if page.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Versions = append(page.Versions, skillVersionFromRow(sqlc.GetSkillVersionRow(row)))
	}
	return page, nil
}
