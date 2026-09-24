package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CreateOAuthCredential(ctx context.Context, tenantID, vaultID string, input CreateOAuthCredentialInput) (Credential, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Credential{}, ErrNotFound
	}
	vault, err := parseID(vaultID)
	if err != nil {
		return Credential{}, ErrNotFound
	}
	if !validVaultName(input.Name) || input.MCPServerURL == "" || !validOAuthMetadata(input.OAuth) {
		return Credential{}, ErrInvalidInput
	}
	if input.OAuth.Refresh == nil && (input.RefreshToken != "" || input.ClientSecret != "") ||
		input.OAuth.Refresh != nil && input.OAuth.Refresh.TokenEndpointAuth == "none" && input.ClientSecret != "" {
		return Credential{}, ErrInvalidInput
	}
	credential := Credential{ID: uuid.NewString(), VaultID: uuid.UUID(vault.Bytes).String(),
		Name: input.Name, AuthType: "mcp_oauth", MCPServerURL: input.MCPServerURL}
	secret := oauthSecret{Version: 1, Metadata: input.OAuth, AccessToken: input.AccessToken,
		RefreshToken: input.RefreshToken, ClientSecret: input.ClientSecret}
	metadata, ciphertext, err := s.sealOAuth(uuid.UUID(tenant.Bytes).String(), credential, secret)
	if err != nil {
		return Credential{}, err
	}
	var created Credential
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.CreateOAuthCredential(ctx, sqlc.CreateOAuthCredentialParams{
			ID: pgtype.UUID{Bytes: uuid.MustParse(credential.ID), Valid: true}, TenantID: tenant, VaultID: vault,
			Name: input.Name, McpServerUrl: input.MCPServerURL, OauthMetadata: metadata, TokenCiphertext: ciphertext,
		})
		if err != nil {
			return err
		}
		created, err = credentialFromRow(sqlc.GetCredentialRow(row))
		if err != nil {
			return err
		}
		return recordWriteAudit(ctx, q, tenantID, "create", "credential", created.ID, created.VaultID, AuditResource{Type: "credential", ID: created.ID, ParentID: created.VaultID})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, errors.New("credential creation failed")
	}
	return created, nil
}

func (s *Store) UpdateOAuthCredential(ctx context.Context, tenantID, vaultID, credentialID string, input UpdateOAuthCredentialInput) (Credential, error) {
	current, err := s.GetCredential(ctx, tenantID, vaultID, credentialID)
	if err != nil {
		return Credential{}, err
	}
	if current.AuthType != "mcp_oauth" {
		return Credential{}, ErrInvalidInput
	}
	tx, credential, secret, err := s.lockOAuth(ctx, tenantID, vaultID, credentialID, "")
	if err != nil {
		return Credential{}, err
	}
	defer tx.Rollback(context.Background())
	if input.AccessToken != nil {
		secret.AccessToken = *input.AccessToken
		secret.Metadata.ExpiresAt = nil
	}
	if input.ExpiresAtSet {
		secret.Metadata.ExpiresAt = input.ExpiresAt
	}
	if update := input.Refresh; update != nil {
		refresh := secret.Metadata.Refresh
		if refresh == nil {
			return Credential{}, ErrInvalidInput
		}
		if update.TokenEndpointAuthType != "" && update.TokenEndpointAuthType != refresh.TokenEndpointAuth {
			return Credential{}, ErrInvalidInput
		}
		if update.ClientSecret != nil {
			if refresh.TokenEndpointAuth == "none" {
				return Credential{}, ErrInvalidInput
			}
			secret.ClientSecret = *update.ClientSecret
		}
		if update.RefreshToken != nil {
			secret.RefreshToken = *update.RefreshToken
		}
		if update.ScopeSet {
			refresh.Scope = update.Scope
		}
	}
	updated, err := s.saveOAuth(ctx, tx, tenantID, credential, secret)
	if err != nil {
		return Credential{}, err
	}
	if err := recordWriteAudit(ctx, s.queries.WithTx(tx), tenantID, "update", "credential", updated.ID, updated.VaultID); err != nil {
		return Credential{}, errors.New("credential update failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, errors.New("credential update failed")
	}
	return updated, nil
}

// Row ownership lasts through refresh or manual replacement. PostgreSQL serializes
// competing updates and deletes, including a parent Vault's cascading deletion.
func (s *Store) lockOAuth(ctx context.Context, tenantID, vaultID, credentialID, destination string) (pgx.Tx, Credential, oauthSecret, error) {
	tenant, e1 := parseID(tenantID)
	vault, e2 := parseID(vaultID)
	id, e3 := parseID(credentialID)
	if e1 != nil || e2 != nil || e3 != nil {
		return nil, Credential{}, oauthSecret{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, Credential{}, oauthSecret{}, errors.New("credential transaction failed")
	}
	fail := func(err error) (pgx.Tx, Credential, oauthSecret, error) {
		_ = tx.Rollback(context.Background())
		return nil, Credential{}, oauthSecret{}, err
	}
	row, err := sqlc.New(tx).GetOAuthCredentialForUpdate(ctx, sqlc.GetOAuthCredentialForUpdateParams{
		TenantID: tenant, VaultID: vault, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(ErrNotFound)
	}
	if err != nil {
		return fail(errors.New("credential lookup failed"))
	}
	credential, err := credentialFromRow(sqlc.GetCredentialRow{ID: row.ID, VaultID: row.VaultID,
		Name: row.Name, AuthType: row.AuthType, McpServerUrl: row.McpServerUrl,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, OauthMetadata: row.OauthMetadata})
	if err != nil {
		return fail(err)
	}
	if destination != "" && credential.MCPServerURL != destination {
		return fail(ErrNotFound)
	}
	secret, err := s.openOAuth(uuid.UUID(tenant.Bytes).String(), credential, row.TokenCiphertext)
	if err != nil {
		return fail(err)
	}
	return tx, credential, secret, nil
}

func (s *Store) saveOAuth(ctx context.Context, tx pgx.Tx, tenantID string, credential Credential, secret oauthSecret) (Credential, error) {
	tenant, _ := parseID(tenantID)
	metadata, ciphertext, err := s.sealOAuth(uuid.UUID(tenant.Bytes).String(), credential, secret)
	if err != nil {
		return Credential{}, err
	}
	vault, _ := parseID(credential.VaultID)
	id, _ := parseID(credential.ID)
	row, err := sqlc.New(tx).UpdateOAuthCredential(ctx, sqlc.UpdateOAuthCredentialParams{
		TenantID: tenant, VaultID: vault, ID: id, McpServerUrl: credential.MCPServerURL,
		OauthMetadata: metadata, TokenCiphertext: ciphertext,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, errors.New("credential update failed")
	}
	return credentialFromRow(sqlc.GetCredentialRow(row))
}
