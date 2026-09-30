package vaultpg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// WithOAuthCredential holds the Credential's row lock, once loaded, until
// apply returns: through an external refresh too.
func (s *Store) WithOAuthCredential(ctx context.Context, key vaults.CredentialKey, apply func(vaults.OAuthTx) error) error {
	tx := &oauthTx{tenantID: key.TenantID, tenant: pgunit.PathID(key.TenantID), vault: pgunit.PathID(key.VaultID), id: pgunit.PathID(key.CredentialID)}
	return translate(s.pool.Transaction(ctx, func(ctx context.Context, t pgx.Tx) error {
		tx.q = sqlc.New(t)
		return apply(tx)
	}))
}

type oauthTx struct {
	q                 *sqlc.Queries
	tenantID          string
	tenant, vault, id pgtype.UUID
}

func (t *oauthTx) LoadOAuthCredential(ctx context.Context) (vaults.Credential, []byte, error) {
	row, err := t.q.GetOAuthCredentialForUpdate(ctx, sqlc.GetOAuthCredentialForUpdateParams{TenantID: t.tenant, VaultID: t.vault, ID: t.id})
	if err != nil {
		return vaults.Credential{}, nil, translate(err)
	}
	credential, err := credentialFromRow(sqlc.GetCredentialRow{ID: row.ID, VaultID: row.VaultID, Name: row.Name, AuthType: row.AuthType,
		McpServerUrl: row.McpServerUrl, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, OauthMetadata: row.OauthMetadata})
	if err != nil {
		return vaults.Credential{}, nil, err
	}
	return credential, row.TokenCiphertext, nil
}

func (t *oauthTx) ApplyOAuthRefresh(ctx context.Context, sealed vaults.SealedOAuth) error {
	_, err := t.update(ctx, sealed)
	return err
}

func (t *oauthTx) ApplyOAuthReplacement(ctx context.Context, sealed vaults.SealedOAuth) (vaults.Credential, error) {
	updated, err := t.update(ctx, sealed)
	if err != nil {
		return vaults.Credential{}, err
	}
	if err := auditpg.RecordWriteAudit(ctx, t.q, t.tenantID, "update", "credential", updated.ID, updated.VaultID); err != nil {
		return vaults.Credential{}, translate(err)
	}
	return updated, nil
}

// update matches the destination the grant is sealed to.
func (t *oauthTx) update(ctx context.Context, sealed vaults.SealedOAuth) (vaults.Credential, error) {
	row, err := t.q.UpdateOAuthCredential(ctx, sqlc.UpdateOAuthCredentialParams{TenantID: t.tenant, VaultID: t.vault, ID: t.id,
		McpServerUrl: sealed.MCPServerURL, OauthMetadata: sealed.Metadata, TokenCiphertext: sealed.Ciphertext})
	if err != nil {
		return vaults.Credential{}, translate(err)
	}
	return credentialFromRow(sqlc.GetCredentialRow(row))
}
