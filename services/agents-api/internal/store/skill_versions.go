package store

import (
	"context"
	"errors"
	"math"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

var ErrDefaultSkillVersion = errors.New("cannot delete the default skill version")

func (s *Store) CreateSkillVersion(ctx context.Context, tenantID, skillID string, archive []byte, makeDefault bool) (SkillVersion, error) {
	tenant, id, err := skillIDs(tenantID, skillID)
	if err != nil {
		return SkillVersion{}, err
	}
	metadata, err := agentskill.Inspect(archive)
	if err != nil {
		return SkillVersion{}, ErrInvalidInput
	}
	var result SkillVersion
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		owner, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: id})
		if err != nil {
			return err
		}
		if owner.NextVersion == math.MaxInt64 {
			return ErrInvalidInput
		}
		result, err = s.saveSkillVersion(ctx, q, tenant, id, owner.NextVersion, metadata, archive)
		if err != nil {
			return err
		}
		return q.AdvanceSkillVersion(ctx, sqlc.AdvanceSkillVersionParams{TenantID: tenant, ID: id, MakeDefault: makeDefault, Name: metadata.Name, Description: metadata.Description})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return result, err
}

func (s *Store) GetSkillVersion(ctx context.Context, tenantID, skillID, version string) (SkillVersion, error) {
	tenant, id, err := skillIDs(tenantID, skillID)
	if err != nil {
		return SkillVersion{}, err
	}
	number, err := skillVersionNumber(version)
	if err != nil {
		return SkillVersion{}, err
	}
	row, err := s.queries.GetSkillVersion(ctx, sqlc.GetSkillVersionParams{TenantID: tenant, SkillID: id, Version: number})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return skillVersionFromRow(row), err
}

// ReadSkillVersion reads metadata and encrypted bytes from one authorized row.
func (s *Store) ReadSkillVersion(ctx context.Context, tenantID, skillID, version string) (SkillVersion, []byte, error) {
	tenant, id, err := skillIDs(tenantID, skillID)
	if err != nil {
		return SkillVersion{}, nil, err
	}
	number, err := skillVersionNumber(version)
	if err != nil {
		return SkillVersion{}, nil, err
	}
	row, err := s.queries.ReadSkillVersion(ctx, sqlc.ReadSkillVersionParams{TenantID: tenant, SkillID: id, Version: number})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return SkillVersion{}, nil, err
	}
	return s.openSkillVersion(row)
}

func (s *Store) openSkillVersion(row sqlc.SkillVersion) (SkillVersion, []byte, error) {
	body, err := s.credentialCipher.OpenSkill(row.Contents, skillBinding(row.TenantID, row.SkillID, row.ID, row.Version))
	if err != nil {
		return SkillVersion{}, nil, err
	}
	metadata := agentskill.Metadata{Type: "inline", Name: row.Name, Description: row.Description}
	if _, err := agentskill.Read(body, metadata); err != nil {
		return SkillVersion{}, nil, ErrInvalidInput
	}
	result := skillVersionFromRow(sqlc.GetSkillVersionRow{ID: row.ID, TenantID: row.TenantID, SkillID: row.SkillID, Version: row.Version, Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt})
	return result, body, nil
}

func (s *Store) DeleteSkillVersion(ctx context.Context, tenantID, skillID, version string) (SkillVersion, error) {
	tenant, id, err := skillIDs(tenantID, skillID)
	if err != nil {
		return SkillVersion{}, err
	}
	number, err := skillVersionNumber(version)
	if err != nil {
		return SkillVersion{}, err
	}
	var result SkillVersion
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		owner, err := q.LockSkill(ctx, sqlc.LockSkillParams{TenantID: tenant, ID: id})
		if err != nil {
			return err
		}
		if owner.DefaultVersion == number {
			return ErrDefaultSkillVersion
		}
		row, err := q.DeleteSkillVersion(ctx, sqlc.DeleteSkillVersionParams{TenantID: tenant, SkillID: id, Version: number})
		if err != nil {
			return err
		}
		result = skillVersionFromRow(sqlc.GetSkillVersionRow(row))
		if owner.LatestVersion == number {
			return q.RefreshLatestSkillVersion(ctx, sqlc.RefreshLatestSkillVersionParams{TenantID: tenant, ID: id})
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return result, err
}

// ReadDefaultSkillVersion selects the pointer and immutable content in one read.
func (s *Store) ReadDefaultSkillVersion(ctx context.Context, tenantID, skillID string) (SkillVersion, []byte, error) {
	tenant, id, err := skillIDs(tenantID, skillID)
	if err != nil {
		return SkillVersion{}, nil, err
	}
	row, err := s.queries.ReadDefaultSkillVersion(ctx, sqlc.ReadDefaultSkillVersionParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return SkillVersion{}, nil, err
	}
	return s.openSkillVersion(row)
}
