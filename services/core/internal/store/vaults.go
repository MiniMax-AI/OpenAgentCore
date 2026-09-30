package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// Vault is a tenant-owned resource, independent of Sessions and engine execution.
type Vault struct {
	ID        string
	TenantID  string
	Name      *string
	Metadata  map[string]string
	CreatedAt time.Time
}

type CreateVaultInput struct {
	Name     *string
	Metadata map[string]string
}

// CreateVault persists the public layer's normalized name. Each call creates a
// distinct resource; this primitive does not define create retry semantics.
func (s *Store) CreateVault(ctx context.Context, tenantID string, input CreateVaultInput) (Vault, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Vault{}, err
	}
	var name pgtype.Text
	if input.Name != nil {
		if !validVaultName(*input.Name) {
			return Vault{}, fmt.Errorf("%w: vault name must contain 1–256 UTF-8 bytes", ErrInvalidInput)
		}
		name = pgtype.Text{String: *input.Name, Valid: true}
	}
	encodedMetadata, err := metadata.Encode(input.Metadata)
	if err != nil {
		return Vault{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	var created Vault
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.CreateVault(ctx, sqlc.CreateVaultParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant,
			Name: name, Metadata: encodedMetadata,
		})
		if err != nil {
			return err
		}
		created, err = vaultFromRow(row)
		if err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "create", "vault", created.ID, "", writeaudit.Resource{Type: "vault", ID: created.ID, ParentID: ""})
	})
	if err != nil {
		return Vault{}, fmt.Errorf("create vault: %w", err)
	}
	return created, nil
}

func validVaultName(name string) bool {
	return len(name) >= 1 && len(name) <= 256 && utf8.ValidString(name)
}

// GetVault scopes every lookup to the authenticated caller's tenant.
func (s *Store) GetVault(ctx context.Context, tenantID, vaultID string) (Vault, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Vault{}, err
	}
	id, err := parseID(vaultID)
	if err != nil {
		return Vault{}, err
	}
	row, err := s.queries.GetVault(ctx, sqlc.GetVaultParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Vault{}, ErrNotFound
	}
	if err != nil {
		return Vault{}, fmt.Errorf("get vault: %w", err)
	}
	return vaultFromRow(row)
}

func vaultFromRow(row sqlc.Vault) (Vault, error) {
	vault := Vault{
		ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String(),
		CreatedAt: row.CreatedAt.Time,
	}
	if row.Name.Valid {
		vault.Name = &row.Name.String
	}
	if err := json.Unmarshal(row.Metadata, &vault.Metadata); err != nil {
		return Vault{}, fmt.Errorf("decode vault metadata: %w", err)
	}
	return vault, nil
}
