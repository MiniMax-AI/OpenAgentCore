// Package skillpg stores Skills and their versions in PostgreSQL. Version
// archives are sealed with the credential key and bound to their tenant,
// Skill, version ID and number.
package skillpg

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the Skills storage and Reader.
type Store struct {
	pool   *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

var (
	_ skills.Storage = (*Store)(nil)
	_ skills.Reader  = (*Store)(nil)
)

// New builds the Skill store. Without a cipher, uploads and content reads
// fail with credentialcrypto.ErrUnavailable and metadata reads still work.
func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

func (s *Store) CreateSkill(ctx context.Context, in skills.NewSkill) (skills.Skill, error) {
	tenant, err := parseTenant(in.TenantID)
	if err != nil {
		return skills.Skill{}, err
	}
	var result skills.Skill
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.CreateSkill(ctx, sqlc.CreateSkillParams{ID: newID(), TenantID: tenant, Name: in.Name, Description: in.Description})
		if err != nil {
			return err
		}
		initial, err := s.insertVersion(ctx, q, row.TenantID, row.ID, 1, in.Name, in.Description, in.Archive)
		if err != nil {
			return err
		}
		result = skillFromRow(row)
		return auditpg.RecordWriteAudit(ctx, q, in.TenantID, "create", "skill", result.ID, "",
			writeaudit.Resource{Type: "skill", ID: result.ID},
			writeaudit.Resource{Type: "skill_version", ID: initial.ID, ParentID: result.ID})
	})
	if err != nil {
		return skills.Skill{}, translate(err)
	}
	return result, nil
}

func (s *Store) CreateVersion(ctx context.Context, in skills.NewVersion) (skills.Version, error) {
	tenant, err := parseTenant(in.TenantID)
	if err != nil {
		return skills.Version{}, err
	}
	var result skills.Version
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		owner, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: pgID(in.SkillID)})
		if err != nil {
			return err
		}
		if owner.NextVersion == math.MaxInt64 {
			return fmt.Errorf("%w: skill version numbers exhausted", skills.ErrInvalidInput)
		}
		result, err = s.insertVersion(ctx, q, owner.TenantID, owner.ID, owner.NextVersion, in.Name, in.Description, in.Archive)
		if err != nil {
			return err
		}
		if err := q.AdvanceSkillVersion(ctx, sqlc.AdvanceSkillVersionParams{TenantID: owner.TenantID, ID: owner.ID, MakeDefault: in.MakeDefault, Name: in.Name, Description: in.Description}); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, in.TenantID, "upload_version", "skill_version", result.ID, result.SkillID,
			writeaudit.Resource{Type: "skill_version", ID: result.ID, ParentID: result.SkillID})
	})
	if err != nil {
		return skills.Version{}, translate(err)
	}
	return result, nil
}

func (s *Store) SetDefaultVersion(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (skills.Skill, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.Skill{}, err
	}
	var result skills.Skill
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: pgID(skillID)}); err != nil {
			return err
		}
		target, err := q.GetSkillVersion(ctx, sqlc.GetSkillVersionParams{TenantID: tenant, SkillID: pgID(skillID), Version: version})
		if err != nil {
			return err
		}
		row, err := q.SetDefaultSkillVersion(ctx, sqlc.SetDefaultSkillVersionParams{TenantID: tenant, ID: pgID(skillID), DefaultVersion: version, Name: target.Name, Description: target.Description})
		if err != nil {
			return err
		}
		result = skillFromRow(row)
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "update_default_version", "skill", result.ID, "")
	})
	if err != nil {
		return skills.Skill{}, translate(err)
	}
	return result, nil
}

func (s *Store) DeleteSkill(ctx context.Context, tenantID string, skillID uuid.UUID) error {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return err
	}
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.DeleteSkill(ctx, sqlc.DeleteSkillParams{TenantID: tenant, ID: pgID(skillID)}); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "skill", skills.FormatID(skillID), "")
	})
	return translate(err)
}

func (s *Store) WithVersionDeletion(ctx context.Context, tenantID string, skillID uuid.UUID, apply func(skills.VersionDeletionTx) error) error {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return err
	}
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		owner, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: pgID(skillID)})
		if err != nil {
			return err
		}
		return apply(&versionDeletion{ctx: ctx, q: q, tenantID: tenantID, skill: owner})
	})
	return translate(err)
}

// versionDeletion is one Skill locked by WithVersionDeletion. ctx is the
// transaction's.
type versionDeletion struct {
	ctx      context.Context
	q        *sqlc.Queries
	tenantID string
	skill    sqlc.Skill
}

func (d *versionDeletion) LoadVersionDeletion(version int64) (skills.VersionDeletionFacts, error) {
	target, err := d.q.GetSkillVersion(d.ctx, sqlc.GetSkillVersionParams{TenantID: d.skill.TenantID, SkillID: d.skill.ID, Version: version})
	if err != nil {
		return skills.VersionDeletionFacts{}, translate(err)
	}
	// Two rows tell whether any version besides the target remains.
	rows, err := d.q.ListSkillVersions(d.ctx, sqlc.ListSkillVersionsParams{TenantID: d.skill.TenantID, SkillID: d.skill.ID, PageLimit: 2})
	if err != nil {
		return skills.VersionDeletionFacts{}, translate(err)
	}
	return skills.VersionDeletionFacts{Skill: skillFromRow(d.skill), Target: versionFromRow(target), OthersRemain: len(rows) > 1}, nil
}

func (d *versionDeletion) ApplyVersionDeletion(decision skills.VersionDeletion) error {
	number := decision.Target.Version
	if decision.DeleteSkill {
		// The versions go with the Skill through the foreign key cascade.
		if _, err := d.q.DeleteSkill(d.ctx, sqlc.DeleteSkillParams{TenantID: d.skill.TenantID, ID: d.skill.ID}); err != nil {
			return translate(err)
		}
	} else {
		if _, err := d.q.DeleteSkillVersion(d.ctx, sqlc.DeleteSkillVersionParams{TenantID: d.skill.TenantID, SkillID: d.skill.ID, Version: number}); err != nil {
			return translate(err)
		}
		if decision.RefreshLatest {
			if err := d.q.RefreshLatestSkillVersion(d.ctx, sqlc.RefreshLatestSkillVersionParams{TenantID: d.skill.TenantID, ID: d.skill.ID}); err != nil {
				return translate(err)
			}
		}
	}
	return translate(auditpg.RecordWriteAudit(d.ctx, d.q, d.tenantID, "delete", "skill_version", decision.Target.ID, decision.Target.SkillID))
}

func (s *Store) Skill(ctx context.Context, tenantID string, id uuid.UUID) (skills.Skill, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.Skill{}, err
	}
	row, err := s.pool.Queries().GetSkill(ctx, sqlc.GetSkillParams{TenantID: tenant, ID: pgID(id)})
	if err != nil {
		return skills.Skill{}, translate(err)
	}
	return skillFromRow(row), nil
}

func (s *Store) Skills(ctx context.Context, tenantID string, page skills.SkillPageQuery) (skills.Page, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.Page{}, err
	}
	// The extra row reports whether another page follows.
	params := sqlc.ListSkillsParams{TenantID: tenant, PageLimit: int32(page.Limit + 1), Ascending: page.Ascending, AfterID: pgtype.UUID{Valid: true}}
	if page.After != nil {
		params.AfterCreated = pgtype.Timestamptz{Time: page.After.CreatedAt, Valid: true}
		params.AfterID = pgID(page.After.ID)
	}
	rows, err := s.pool.Queries().ListSkills(ctx, params)
	if err != nil {
		return skills.Page{}, translate(err)
	}
	result := skills.Page{Skills: make([]skills.Skill, 0, min(page.Limit, len(rows))), HasMore: len(rows) > page.Limit}
	for _, row := range rows[:min(page.Limit, len(rows))] {
		result.Skills = append(result.Skills, skillFromRow(row))
	}
	return result, nil
}

func (s *Store) Version(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (skills.Version, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.Version{}, err
	}
	row, err := s.pool.Queries().GetSkillVersion(ctx, sqlc.GetSkillVersionParams{TenantID: tenant, SkillID: pgID(skillID), Version: version})
	if err != nil {
		return skills.Version{}, translate(err)
	}
	return versionFromRow(row), nil
}

func (s *Store) VersionByID(ctx context.Context, tenantID string, id uuid.UUID) (skills.Version, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.Version{}, err
	}
	row, err := s.pool.Queries().GetSkillVersionByID(ctx, sqlc.GetSkillVersionByIDParams{TenantID: tenant, ID: pgID(id)})
	if err != nil {
		return skills.Version{}, translate(err)
	}
	return versionFromRow(sqlc.GetSkillVersionRow(row)), nil
}

func (s *Store) Versions(ctx context.Context, tenantID string, skillID uuid.UUID, page skills.VersionPageQuery) (skills.VersionPage, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.VersionPage{}, err
	}
	rows, err := s.pool.Queries().ListSkillVersions(ctx, sqlc.ListSkillVersionsParams{
		TenantID: tenant, SkillID: pgID(skillID), PageLimit: int32(page.Limit + 1), Ascending: page.Ascending,
		AfterVersion: pgtype.Int8{Int64: page.AfterVersion, Valid: page.AfterVersion > 0},
	})
	if err != nil {
		return skills.VersionPage{}, translate(err)
	}
	result := skills.VersionPage{Versions: make([]skills.Version, 0, min(page.Limit, len(rows))), HasMore: len(rows) > page.Limit}
	for _, row := range rows[:min(page.Limit, len(rows))] {
		result.Versions = append(result.Versions, versionFromRow(sqlc.GetSkillVersionRow(row)))
	}
	return result, nil
}

func (s *Store) VersionContent(ctx context.Context, tenantID string, skillID uuid.UUID, version int64) (skills.Content, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.Content{}, err
	}
	row, err := s.pool.Queries().ReadSkillVersion(ctx, sqlc.ReadSkillVersionParams{TenantID: tenant, SkillID: pgID(skillID), Version: version})
	if err != nil {
		return skills.Content{}, translate(err)
	}
	return s.open(row)
}

func (s *Store) DefaultVersionContent(ctx context.Context, tenantID string, skillID uuid.UUID) (skills.Content, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return skills.Content{}, err
	}
	row, err := s.pool.Queries().ReadDefaultSkillVersion(ctx, sqlc.ReadDefaultSkillVersionParams{TenantID: tenant, ID: pgID(skillID)})
	if err != nil {
		return skills.Content{}, translate(err)
	}
	return s.open(row)
}

// insertVersion seals the archive under a new version ID and stores it.
func (s *Store) insertVersion(ctx context.Context, q *sqlc.Queries, tenant, skill pgtype.UUID, version int64, name, description string, archive []byte) (skills.Version, error) {
	if s.cipher == nil {
		return skills.Version{}, credentialcrypto.ErrUnavailable
	}
	id := newID()
	body, err := s.cipher.SealSkill(archive, credentialcrypto.NewSkillBinding(tenant.Bytes, skill.Bytes, id.Bytes, version))
	if err != nil {
		return skills.Version{}, err
	}
	row, err := q.CreateSkillVersion(ctx, sqlc.CreateSkillVersionParams{ID: id, TenantID: tenant, SkillID: skill, Version: version, Name: name, Description: description, Contents: body})
	if err != nil {
		return skills.Version{}, err
	}
	return versionFromRow(sqlc.GetSkillVersionRow(row)), nil
}

func (s *Store) open(row sqlc.SkillVersion) (skills.Content, error) {
	if s.cipher == nil {
		return skills.Content{}, credentialcrypto.ErrUnavailable
	}
	archive, err := s.cipher.OpenSkill(row.Contents, credentialcrypto.NewSkillBinding(row.TenantID.Bytes, row.SkillID.Bytes, row.ID.Bytes, row.Version))
	if err != nil {
		return skills.Content{}, err
	}
	version := versionFromRow(sqlc.GetSkillVersionRow{ID: row.ID, TenantID: row.TenantID, SkillID: row.SkillID, Version: row.Version, Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt})
	return skills.Content{Version: version, Archive: archive}, nil
}

// translate turns the database outcomes callers act on into the domain's
// errors and returns every other error as it is.
func translate(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return skills.ErrNotFound
	case pgunit.IsUnstorableText(err):
		return textvalue.ErrUnstorable
	}
	return err
}

func parseTenant(value string) (pgtype.UUID, error) {
	tenant, err := pgunit.ParseID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: tenant ID", skills.ErrInvalidInput)
	}
	return tenant, nil
}

func pgID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func newID() pgtype.UUID { return pgID(uuid.New()) }

func skillFromRow(row sqlc.Skill) skills.Skill {
	return skills.Skill{ID: skills.FormatID(row.ID.Bytes), Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt.Time, DefaultVersion: row.DefaultVersion, LatestVersion: row.LatestVersion}
}

func versionFromRow(row sqlc.GetSkillVersionRow) skills.Version {
	return skills.Version{ID: skills.FormatVersionID(row.ID.Bytes), SkillID: skills.FormatID(row.SkillID.Bytes), Version: row.Version, Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt.Time}
}
