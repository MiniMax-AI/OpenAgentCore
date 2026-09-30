package vaultpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// WithOAuthCredential holds the Credential's row lock, once loaded, until
// apply returns: through an external refresh too.
func (s *Store) WithOAuthCredential(ctx context.Context, key vaults.CredentialKey, apply func(vaults.OAuthTx) error) error {
	tx := &oauthTx{store: s, tenantID: key.TenantID, tenant: pgunit.PathID(key.TenantID), vault: pgunit.PathID(key.VaultID), id: pgunit.PathID(key.CredentialID)}
	return translate(s.pool.Transaction(ctx, func(ctx context.Context, t pgx.Tx) error {
		tx.q = sqlc.New(t)
		return apply(tx)
	}))
}

type oauthTx struct {
	store             *Store
	q                 *sqlc.Queries
	tenantID          string
	tenant, vault, id pgtype.UUID
	// loaded is the locked Credential, which the applied grant is sealed to.
	loaded *vaults.Credential
}

func (t *oauthTx) LoadOAuthGrant(ctx context.Context, destination string) (vaults.OAuthGrant, error) {
	row, err := t.q.GetOAuthCredentialForUpdate(ctx, sqlc.GetOAuthCredentialForUpdateParams{TenantID: t.tenant, VaultID: t.vault, ID: t.id})
	if err != nil {
		return vaults.OAuthGrant{}, translate(err)
	}
	credential, err := credentialFromRow(sqlc.GetCredentialRow{ID: row.ID, VaultID: row.VaultID, Name: row.Name, AuthType: row.AuthType,
		McpServerUrl: row.McpServerUrl, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, OauthMetadata: row.OauthMetadata})
	if err != nil {
		return vaults.OAuthGrant{}, err
	}
	if destination != "" && credential.MCPServerURL != destination {
		return vaults.OAuthGrant{}, vaults.ErrNotFound
	}
	grant, err := t.store.openOAuth(t.scope(credential), credential.OAuth, row.TokenCiphertext)
	if err != nil {
		return vaults.OAuthGrant{}, err
	}
	t.loaded = &credential
	return grant, nil
}

func (t *oauthTx) ApplyOAuthRefresh(ctx context.Context, grant vaults.OAuthGrant) error {
	_, err := t.update(ctx, grant)
	return err
}

func (t *oauthTx) ApplyOAuthReplacement(ctx context.Context, grant vaults.OAuthGrant) (vaults.Credential, error) {
	updated, err := t.update(ctx, grant)
	if err != nil {
		return vaults.Credential{}, err
	}
	if err := auditpg.RecordWriteAudit(ctx, t.q, t.tenantID, "update", "credential", updated.ID, updated.VaultID); err != nil {
		return vaults.Credential{}, translate(err)
	}
	return updated, nil
}

// update seals the grant to the loaded Credential and matches the destination
// it is sealed to.
func (t *oauthTx) update(ctx context.Context, grant vaults.OAuthGrant) (vaults.Credential, error) {
	if t.loaded == nil {
		return vaults.Credential{}, errors.New("OAuth credential was not loaded")
	}
	metadata, ciphertext, err := t.store.sealOAuth(t.scope(*t.loaded), grant)
	if err != nil {
		return vaults.Credential{}, err
	}
	row, err := t.q.UpdateOAuthCredential(ctx, sqlc.UpdateOAuthCredentialParams{TenantID: t.tenant, VaultID: t.vault, ID: t.id,
		McpServerUrl: t.loaded.MCPServerURL, OauthMetadata: metadata, TokenCiphertext: ciphertext})
	if err != nil {
		return vaults.Credential{}, translate(err)
	}
	return credentialFromRow(sqlc.GetCredentialRow(row))
}

// scope is the seal scope of the locked Credential's grant.
func (t *oauthTx) scope(credential vaults.Credential) credentialcrypto.Binding {
	return binding(t.tenant, pgunit.PathID(credential.VaultID), pgunit.PathID(credential.ID), vaults.AuthMCPOAuth, credential.MCPServerURL)
}
