package vaultpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// CreateCredential admits the owning Vault in the insert itself, so a missing
// or foreign Vault stores nothing and is ErrNotFound, as is a Vault deleted
// while the insert waits on it. A malformed new Credential ID is
// ErrInvalidInput.
func (s *Store) CreateCredential(ctx context.Context, credential vaults.NewCredential) (vaults.Credential, error) {
	if _, err := pgunit.ParseID(credential.CredentialID); err != nil {
		return vaults.Credential{}, vaults.ErrInvalidInput
	}
	var created vaults.Credential
	err := s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		row, err := insertCredential(ctx, q, credential)
		// A foreign-key violation means the Vault was deleted after the insert read it.
		var constraint *pgconn.PgError
		if errors.As(err, &constraint) && constraint.Code == "23503" {
			return vaults.ErrNotFound
		}
		if err != nil {
			return err
		}
		if created, err = credentialFromRow(row); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, credential.TenantID, "create", "credential", created.ID, created.VaultID,
			writeaudit.Resource{Type: "credential", ID: created.ID, ParentID: created.VaultID})
	})
	if err != nil {
		return vaults.Credential{}, err
	}
	return created, nil
}

func insertCredential(ctx context.Context, q *sqlc.Queries, credential vaults.NewCredential) (sqlc.GetCredentialRow, error) {
	id, tenant, vault := pgunit.PathID(credential.CredentialID), pgunit.PathID(credential.TenantID), pgunit.PathID(credential.VaultID)
	switch credential.AuthType {
	case vaults.AuthStaticBearer:
		row, err := q.CreateStaticCredential(ctx, sqlc.CreateStaticCredentialParams{ID: id, TenantID: tenant, VaultID: vault,
			Name: credential.Name, McpServerUrl: credential.MCPServerURL, TokenCiphertext: credential.Ciphertext})
		return sqlc.GetCredentialRow(row), err
	case vaults.AuthMCPOAuth:
		row, err := q.CreateOAuthCredential(ctx, sqlc.CreateOAuthCredentialParams{ID: id, TenantID: tenant, VaultID: vault,
			Name: credential.Name, McpServerUrl: credential.MCPServerURL, OauthMetadata: credential.OAuthMetadata, TokenCiphertext: credential.Ciphertext})
		return sqlc.GetCredentialRow(row), err
	}
	return sqlc.GetCredentialRow{}, errors.New("unknown credential authentication type")
}

func (s *Store) GetCredential(ctx context.Context, tenantID, vaultID, credentialID string) (vaults.Credential, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return vaults.Credential{}, vaults.ErrInvalidInput
	}
	vault, err := pgunit.ParseID(vaultID)
	if err != nil {
		return vaults.Credential{}, vaults.ErrInvalidInput
	}
	id, err := pgunit.ParseID(credentialID)
	if err != nil {
		return vaults.Credential{}, vaults.ErrInvalidInput
	}
	return getCredential(ctx, s.pool.Queries(), tenant, vault, id)
}

func getCredential(ctx context.Context, q *sqlc.Queries, tenant, vault, id pgtype.UUID) (vaults.Credential, error) {
	row, err := q.GetCredential(ctx, sqlc.GetCredentialParams{TenantID: tenant, VaultID: vault, ID: id})
	if err != nil {
		return vaults.Credential{}, translate(err)
	}
	return credentialFromRow(row)
}

// ListCredentials reads the parent Vault, the cursor and the page from one
// snapshot. A parent ID that cannot name a Vault is ErrNotFound.
func (s *Store) ListCredentials(ctx context.Context, tenantID, vaultID string, query vaults.PageQuery) (vaults.CredentialPage, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return vaults.CredentialPage{}, vaults.ErrInvalidInput
	}
	vault := pgunit.PathID(vaultID)
	var page vaults.CredentialPage
	err = s.read(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		// An inaccessible parent is not an authorized empty collection.
		if _, err := getVault(ctx, q, tenant, vault); err != nil {
			return err
		}
		statuses, err := query.Validate()
		if err != nil {
			return err
		}
		params := sqlc.ListCredentialsParams{TenantID: tenant, VaultID: vault, PageLimit: int32(query.Limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: query.Ascending, Statuses: statuses}
		if query.After != "" {
			// A cursor that cannot name a Credential of this Vault follows the
			// missing-cursor path.
			after, err := getCredential(ctx, q, tenant, vault, pgunit.PathID(query.After))
			if err != nil {
				return err
			}
			params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
			params.AfterID = pgunit.PathID(after.ID)
		}
		rows, err := q.ListCredentials(ctx, params)
		if err != nil {
			return err
		}
		page = vaults.CredentialPage{Credentials: make([]vaults.Credential, 0, min(query.Limit, len(rows)))}
		if len(rows) > query.Limit {
			page.NextCursor = uuid.UUID(rows[query.Limit-1].ID.Bytes).String()
			rows = rows[:query.Limit]
		}
		for _, row := range rows {
			credential, err := credentialFromRow(sqlc.GetCredentialRow(row))
			if err != nil {
				return err
			}
			page.Credentials = append(page.Credentials, credential)
		}
		return nil
	})
	if err != nil {
		return vaults.CredentialPage{}, err
	}
	return page, nil
}

// ReplaceStaticToken matches the destination the token is sealed to, so a
// concurrent change of scope stores nothing.
func (s *Store) ReplaceStaticToken(ctx context.Context, replacement vaults.StaticTokenReplacement) (vaults.Credential, error) {
	var updated vaults.Credential
	err := s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		row, err := q.UpdateStaticCredential(ctx, sqlc.UpdateStaticCredentialParams{
			TenantID: pgunit.PathID(replacement.TenantID), VaultID: pgunit.PathID(replacement.VaultID), ID: pgunit.PathID(replacement.CredentialID),
			McpServerUrl: replacement.MCPServerURL, TokenCiphertext: replacement.Ciphertext,
		})
		if err != nil {
			return err
		}
		updated, err = credentialFromRow(sqlc.GetCredentialRow(row))
		if err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, replacement.TenantID, "update", "credential", updated.ID, updated.VaultID)
	})
	if err != nil {
		return vaults.Credential{}, err
	}
	return updated, nil
}

// DeleteCredential removes the sealed secret without reading it.
func (s *Store) DeleteCredential(ctx context.Context, key vaults.CredentialKey) (string, error) {
	vault := pgunit.PathID(key.VaultID)
	var deleted string
	err := s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		id, err := q.DeleteCredential(ctx, sqlc.DeleteCredentialParams{TenantID: pgunit.PathID(key.TenantID), VaultID: vault, ID: pgunit.PathID(key.CredentialID)})
		if err != nil {
			return err
		}
		deleted = uuid.UUID(id.Bytes).String()
		return auditpg.RecordWriteAudit(ctx, q, key.TenantID, "delete", "credential", deleted, uuid.UUID(vault.Bytes).String())
	})
	if err != nil {
		return "", err
	}
	return deleted, nil
}

func credentialFromRow(row sqlc.GetCredentialRow) (vaults.Credential, error) {
	credential := vaults.Credential{
		ID: uuid.UUID(row.ID.Bytes).String(), VaultID: uuid.UUID(row.VaultID.Bytes).String(),
		Name: row.Name, AuthType: row.AuthType, MCPServerURL: row.McpServerUrl,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.AuthType == vaults.AuthMCPOAuth {
		credential.OAuth = &vaults.OAuthMetadata{}
		if err := json.Unmarshal(row.OauthMetadata, credential.OAuth); err != nil {
			return vaults.Credential{}, errors.New("invalid stored OAuth metadata")
		}
	}
	return credential, nil
}
