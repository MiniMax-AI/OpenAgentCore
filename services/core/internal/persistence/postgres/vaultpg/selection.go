package vaultpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func (s *Store) CountOwnedVaults(ctx context.Context, tenantID string, vaultIDs []string) (int, error) {
	owned, err := s.pool.Queries().GetAttachedVaultIDs(ctx, sqlc.GetAttachedVaultIDsParams{TenantID: pgunit.PathID(tenantID), VaultIds: pathIDs(vaultIDs)})
	if err != nil {
		return 0, translate(err)
	}
	return len(owned), nil
}

func (s *Store) FindMCPCredentials(ctx context.Context, query vaults.MCPCredentialQuery) ([]vaults.MCPCredentialMatch, error) {
	var credential pgtype.UUID
	if query.CredentialID != "" {
		credential = pgunit.PathID(query.CredentialID)
	}
	rows, err := s.pool.Queries().FindMCPCredentials(ctx, sqlc.FindMCPCredentialsParams{TenantID: pgunit.PathID(query.TenantID),
		VaultIds: pathIDs(query.VaultIDs), McpServerUrl: query.ServerURL, CredentialID: credential})
	if err != nil {
		return nil, translate(err)
	}
	matches := make([]vaults.MCPCredentialMatch, 0, len(rows))
	for _, row := range rows {
		matches = append(matches, vaults.MCPCredentialMatch{VaultID: uuid.UUID(row.VaultID.Bytes).String(), CredentialID: uuid.UUID(row.ID.Bytes).String(),
			AuthType: row.AuthType, MCPServerURL: row.McpServerUrl})
	}
	return matches, nil
}

// StaticToken is the only read of a static token. Resource reads never select
// ciphertext.
func (s *Store) StaticToken(ctx context.Context, query vaults.StaticTokenQuery) (string, error) {
	tenant, vault, id := pgunit.PathID(query.TenantID), pgunit.PathID(query.VaultID), pgunit.PathID(query.CredentialID)
	ciphertext, err := s.pool.Queries().GetMCPStaticCredentialCiphertext(ctx, sqlc.GetMCPStaticCredentialCiphertextParams{
		TenantID: tenant, VaultIds: pathIDs(query.VaultIDs), VaultID: vault, CredentialID: id, McpServerUrl: query.MCPServerURL,
	})
	if err != nil {
		return "", translate(err)
	}
	return s.openStatic(binding(tenant, vault, id, vaults.AuthStaticBearer, query.MCPServerURL), ciphertext)
}

func pathIDs(ids []string) []pgtype.UUID {
	result := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		result = append(result, pgunit.PathID(id))
	}
	return result
}
