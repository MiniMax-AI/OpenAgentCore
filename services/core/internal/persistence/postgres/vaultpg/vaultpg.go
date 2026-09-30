// Package vaultpg stores Vaults and Credentials in PostgreSQL.
package vaultpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// Store runs on pooled connections. Credential operations never need the
// execution lease: an OAuth refresh holds its Credential's row lock for up to
// the refresh bound and must not hold up execution-owner work.
type Store struct{ pool *pgunit.Pool }

var _ vaults.Storage = (*Store)(nil)

func New(pool *pgunit.Pool) *Store { return &Store{pool: pool} }

// write runs apply in one pooled transaction and translates its outcome: no
// row or a missing parent is ErrNotFound, audit provenance and unstorable-text rejections keep
// their shared errors, and any other failure is the operation's opaque
// failure, never database text.
func (s *Store) write(ctx context.Context, failure string, apply func(context.Context, *sqlc.Queries) error) error {
	err := s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return apply(ctx, sqlc.New(tx))
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, vaults.ErrNotFound):
		return vaults.ErrNotFound
	case errors.Is(err, writeaudit.ErrInvalidSource), errors.Is(err, adminaudit.ErrInvalidSource):
		return err
	case pgunit.IsUnstorableText(err):
		return textvalue.ErrUnstorable
	default:
		return errors.New(failure)
	}
}

// read runs apply in one snapshot. apply returns translated errors; a failure
// to begin or commit is the operation's opaque failure.
func (s *Store) read(ctx context.Context, failure string, apply func(context.Context, *sqlc.Queries) error) error {
	var applied error
	err := s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		applied = apply(ctx, sqlc.New(tx))
		return applied
	})
	if err != nil && applied == nil {
		return errors.New(failure)
	}
	return err
}

func (s *Store) CreateVault(ctx context.Context, vault vaults.NewVault) (vaults.Vault, error) {
	tenant, err := pgunit.ParseID(vault.TenantID)
	if err != nil {
		return vaults.Vault{}, vaults.ErrInvalidInput
	}
	var name pgtype.Text
	if vault.Name != nil {
		name = pgtype.Text{String: *vault.Name, Valid: true}
	}
	var created vaults.Vault
	err = s.write(ctx, "vault creation failed", func(ctx context.Context, q *sqlc.Queries) error {
		row, err := q.CreateVault(ctx, sqlc.CreateVaultParams{ID: newID(), TenantID: tenant, Name: name, Metadata: vault.Metadata})
		if err != nil {
			return err
		}
		created, err = vaultFromRow(row)
		if err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, vault.TenantID, "create", "vault", created.ID, "", writeaudit.Resource{Type: "vault", ID: created.ID})
	})
	if err != nil {
		return vaults.Vault{}, err
	}
	return created, nil
}

func (s *Store) GetVault(ctx context.Context, tenantID, vaultID string) (vaults.Vault, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return vaults.Vault{}, vaults.ErrInvalidInput
	}
	id, err := pgunit.ParseID(vaultID)
	if err != nil {
		return vaults.Vault{}, vaults.ErrInvalidInput
	}
	return getVault(ctx, s.pool.Queries(), tenant, id)
}

func getVault(ctx context.Context, q *sqlc.Queries, tenant, id pgtype.UUID) (vaults.Vault, error) {
	row, err := q.GetVault(ctx, sqlc.GetVaultParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return vaults.Vault{}, vaults.ErrNotFound
	}
	if err != nil {
		return vaults.Vault{}, errors.New("vault lookup failed")
	}
	return vaultFromRow(row)
}

// ListVaults reads the cursor and the page from one snapshot.
func (s *Store) ListVaults(ctx context.Context, tenantID string, query vaults.PageQuery) (vaults.VaultPage, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return vaults.VaultPage{}, vaults.ErrInvalidInput
	}
	statuses, err := query.Validate()
	if err != nil {
		return vaults.VaultPage{}, err
	}
	var page vaults.VaultPage
	err = s.read(ctx, "vault list failed", func(ctx context.Context, q *sqlc.Queries) error {
		params := sqlc.ListVaultsParams{TenantID: tenant, PageLimit: int32(query.Limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: query.Ascending, Statuses: statuses}
		if query.After != "" {
			// A cursor that cannot name a Vault follows the missing-cursor path.
			after, err := getVault(ctx, q, tenant, pgunit.PathID(query.After))
			if err != nil {
				return err
			}
			params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
			params.AfterID = pgunit.PathID(after.ID)
		}
		rows, err := q.ListVaults(ctx, params)
		if err != nil {
			return errors.New("vault list failed")
		}
		page = vaults.VaultPage{Vaults: make([]vaults.Vault, 0, min(query.Limit, len(rows)))}
		if len(rows) > query.Limit {
			page.NextCursor = uuid.UUID(rows[query.Limit-1].ID.Bytes).String()
			rows = rows[:query.Limit]
		}
		for _, row := range rows {
			vault, err := vaultFromRow(row)
			if err != nil {
				return err
			}
			page.Vaults = append(page.Vaults, vault)
		}
		return nil
	})
	if err != nil {
		return vaults.VaultPage{}, err
	}
	return page, nil
}

// DeleteVault relies on the owning foreign key to remove every stored Credential.
func (s *Store) DeleteVault(ctx context.Context, tenantID, vaultID string) (string, error) {
	var deleted string
	err := s.write(ctx, "vault deletion failed", func(ctx context.Context, q *sqlc.Queries) error {
		id, err := q.DeleteVault(ctx, sqlc.DeleteVaultParams{TenantID: pgunit.PathID(tenantID), ID: pgunit.PathID(vaultID)})
		if err != nil {
			return err
		}
		deleted = uuid.UUID(id.Bytes).String()
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "vault", deleted, "")
	})
	if err != nil {
		return "", err
	}
	return deleted, nil
}

func vaultFromRow(row sqlc.Vault) (vaults.Vault, error) {
	vault := vaults.Vault{ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String(), CreatedAt: row.CreatedAt.Time}
	if row.Name.Valid {
		vault.Name = &row.Name.String
	}
	if err := json.Unmarshal(row.Metadata, &vault.Metadata); err != nil {
		return vaults.Vault{}, errors.New("invalid stored vault metadata")
	}
	return vault, nil
}

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }
