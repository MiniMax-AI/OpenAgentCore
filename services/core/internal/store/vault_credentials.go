package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Credential contains only public metadata. Secret ciphertext is never selected
// by resource reads; decryption belongs to scoped execution lookup only.
type Credential struct {
	ID, VaultID, Name, AuthType, MCPServerURL string
	CreatedAt, UpdatedAt                      time.Time
	OAuth                                     *OAuthMetadata
}

type CreateStaticCredentialInput struct {
	Name, MCPServerURL, Token string
}

// NewWithCredentialCipher configures immutable credential encryption before the
// Store is published. A nil cipher leaves non-secret resource operations available.
func NewWithCredentialCipher(pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) *Store {
	refresher, _ := oauthrefresh.NewClient(nil)
	return NewWithCredentialCipherAndOAuthRefresh(pool, cipher, refresher)
}

func (s *Store) CreateStaticCredential(ctx context.Context, tenantID, vaultID string, input CreateStaticCredentialInput) (Credential, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Credential{}, err
	}
	vault := pgunit.PathID(vaultID)
	if !validVaultName(input.Name) || input.MCPServerURL == "" {
		return Credential{}, ErrInvalidInput
	}
	if s.credentialCipher == nil {
		return Credential{}, credentialcrypto.ErrUnavailable
	}
	id := uuid.New()
	binding := credentialcrypto.Binding{TenantID: uuid.UUID(tenant.Bytes).String(), VaultID: uuid.UUID(vault.Bytes).String(), CredentialID: id.String(), AuthType: "static_bearer", Destination: input.MCPServerURL}
	ciphertext, err := s.credentialCipher.Seal([]byte(input.Token), binding)
	if err != nil {
		return Credential{}, errors.New("credential encryption failed")
	}
	var created Credential
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.CreateStaticCredential(ctx, sqlc.CreateStaticCredentialParams{
			ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: tenant, VaultID: vault,
			Name: input.Name, McpServerUrl: input.MCPServerURL, TokenCiphertext: ciphertext,
		})
		if err != nil {
			return err
		}
		created, err = credentialFromRow(sqlc.GetCredentialRow(row))
		if err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "create", "credential", created.ID, created.VaultID, writeaudit.Resource{Type: "credential", ID: created.ID, ParentID: created.VaultID})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("create credential: %w", err)
	}
	return created, nil
}

func (s *Store) GetCredential(ctx context.Context, tenantID, vaultID, credentialID string) (Credential, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Credential{}, err
	}
	vault, err := parseID(vaultID)
	if err != nil {
		return Credential{}, err
	}
	id, err := parseID(credentialID)
	if err != nil {
		return Credential{}, err
	}
	row, err := s.queries.GetCredential(ctx, sqlc.GetCredentialParams{TenantID: tenant, VaultID: vault, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("get credential: %w", err)
	}
	return credentialFromRow(row)
}

func credentialFromRow(row sqlc.GetCredentialRow) (Credential, error) {
	result := Credential{
		ID: uuid.UUID(row.ID.Bytes).String(), VaultID: uuid.UUID(row.VaultID.Bytes).String(),
		Name: row.Name, AuthType: row.AuthType, MCPServerURL: row.McpServerUrl,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.AuthType == "mcp_oauth" {
		result.OAuth = &OAuthMetadata{}
		if err := json.Unmarshal(row.OauthMetadata, result.OAuth); err != nil {
			return Credential{}, errors.New("invalid stored OAuth metadata")
		}
	}
	return result, nil
}

// NewWithCredentialCipherAndOAuthRefresh configures the execution-only refresh boundary.
func NewWithCredentialCipherAndOAuthRefresh(pool *pgxpool.Pool, cipher *credentialcrypto.Cipher, refresher oauthrefresh.Refresher) *Store {
	s := New(pool)
	s.credentialCipher, s.oauthRefresher = cipher, refresher
	return s
}
