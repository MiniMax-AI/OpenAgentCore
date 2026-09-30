// Package vaultpg stores Vaults and Credentials in PostgreSQL.
package vaultpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// Store runs on pooled connections and seals and opens the Credential
// secrets it stores. Credential operations never need the execution lease: an
// OAuth refresh holds its Credential's row lock for up to the refresh bound
// and must not hold up execution-owner work.
type Store struct {
	pool   *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

var _ vaults.Storage = (*Store)(nil)

// New returns a Store. Without a credential key (cipher nil), operations that
// seal or open a secret fail with credentialcrypto.ErrUnavailable; Vaults,
// Credential metadata, selection and deletion keep working.
func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

// write runs apply in one pooled transaction and translates its outcome.
func (s *Store) write(ctx context.Context, apply func(context.Context, *sqlc.Queries) error) error {
	return translate(s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return apply(ctx, sqlc.New(tx))
	}))
}

// read runs apply in one snapshot and translates its outcome.
func (s *Store) read(ctx context.Context, apply func(context.Context, *sqlc.Queries) error) error {
	return translate(s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return apply(ctx, sqlc.New(tx))
	}))
}

// translate turns the database outcomes a caller acts on into domain errors:
// no row is ErrNotFound and text PostgreSQL cannot store is
// textvalue.ErrUnstorable. Any other error returns as is.
func translate(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return vaults.ErrNotFound
	case pgunit.IsUnstorableText(err):
		return textvalue.ErrUnstorable
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
	err = s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
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
	if err != nil {
		return vaults.Vault{}, translate(err)
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
	err = s.read(ctx, func(ctx context.Context, q *sqlc.Queries) error {
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
			return err
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
	err := s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
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
