package store

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

func (c *assetCopier) vault(ctx context.Context, id string) (string, error) {
	source, err := parseID(id)
	if err != nil {
		return "", ErrNotFound
	}
	row, err := c.q.LockAdminCopyVault(ctx, sqlc.LockAdminCopyVaultParams{TenantID: c.source, ID: source})
	if err != nil {
		return "", err
	}
	target := copyUUID()
	_, err = c.q.CreateAdminCopyVault(ctx, sqlc.CreateAdminCopyVaultParams{ID: target, TenantID: c.target, Name: row.Name, Metadata: row.Metadata, Status: row.Status})
	if err != nil {
		return "", err
	}
	targetID := c.add("vault", id, copyID(target), "")
	credentials, err := c.q.AdminCopyCredentialIDs(ctx, sqlc.AdminCopyCredentialIDsParams{TenantID: c.source, ID: source})
	if err != nil {
		return "", err
	}
	for _, credential := range credentials {
		if _, err := c.copy(ctx, "credential", copyID(credential), targetID); err != nil {
			return "", err
		}
	}
	return targetID, nil
}

func (c *assetCopier) credential(ctx context.Context, id, vault string) (string, error) {
	source, err := parseID(id)
	if err != nil {
		return "", ErrNotFound
	}
	targetVault, err := parseID(vault)
	if err != nil {
		return "", ErrNotFound
	}
	vault = copyID(targetVault)
	if _, err := c.q.LockAdminCopyVault(ctx, sqlc.LockAdminCopyVaultParams{TenantID: c.target, ID: targetVault}); err != nil {
		return "", err
	}
	row, err := c.q.GetAdminCopyCredential(ctx, sqlc.GetAdminCopyCredentialParams{TenantID: c.source, ID: source})
	if err != nil {
		return "", err
	}
	target := copyUUID()
	var encrypted []byte
	switch row.AuthType {
	case "static_bearer":
		binding := credentialcrypto.Binding{TenantID: c.sourceTenant, VaultID: copyID(row.VaultID), CredentialID: id, AuthType: row.AuthType, Destination: row.McpServerUrl}
		plaintext, err := c.s.credentialCipher.Open(row.TokenCiphertext, binding)
		if err != nil {
			return "", err
		}
		binding.TenantID, binding.VaultID, binding.CredentialID = c.targetTenant, vault, copyID(target)
		encrypted, err = c.s.credentialCipher.Seal(plaintext, binding)
		if err != nil {
			return "", err
		}
	case "mcp_oauth":
		credential, err := credentialFromRow(sqlc.GetCredentialRow{ID: row.ID, VaultID: row.VaultID, Name: row.Name, AuthType: row.AuthType, McpServerUrl: row.McpServerUrl, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, OauthMetadata: row.OauthMetadata})
		if err != nil {
			return "", err
		}
		secret, err := c.s.openOAuth(c.sourceTenant, credential, row.TokenCiphertext)
		if err != nil {
			return "", err
		}
		if secret.Metadata.Refresh != nil {
			c.mapped["credential:"+id] = ""
			c.result.Skipped = append(c.result.Skipped, AssetCopySkipped{Type: "credential", SourceID: id, Reason: "OAuth refresh tokens may rotate; independent copies could invalidate each other"})
			return "", nil
		}
		credential.ID, credential.VaultID = copyID(target), vault
		_, encrypted, err = c.s.sealOAuth(c.targetTenant, credential, secret)
		if err != nil {
			return "", err
		}
	default:
		return "", ErrInvalidInput
	}
	_, err = c.q.CreateAdminCopyCredential(ctx, sqlc.CreateAdminCopyCredentialParams{ID: target, TenantID: c.target, VaultID: targetVault, Name: row.Name, AuthType: row.AuthType, McpServerUrl: row.McpServerUrl, TokenCiphertext: encrypted, OauthMetadata: row.OauthMetadata, Status: row.Status})
	if err != nil {
		return "", err
	}
	return c.add("credential", id, copyID(target), vault), nil
}

func (c *assetCopier) agent(ctx context.Context, id string) (string, error) {
	source, err := parseID(id)
	if err != nil {
		return "", ErrNotFound
	}
	row, err := c.q.LockAgent(ctx, sqlc.LockAgentParams{TenantID: c.source, ID: source})
	if err != nil {
		return "", err
	}
	var configuration map[string]json.RawMessage
	if json.Unmarshal(row.Configuration, &configuration) != nil {
		return "", ErrInvalidInput
	}
	var tools []map[string]json.RawMessage
	if raw := configuration["tools"]; len(raw) > 0 && json.Unmarshal(raw, &tools) != nil {
		return "", ErrInvalidInput
	}
	for _, tool := range tools {
		var kind string
		if json.Unmarshal(tool["type"], &kind) != nil {
			return "", ErrInvalidInput
		}
		if kind != "mcp" {
			continue
		}
		raw, exists := tool["credential_id"]
		if !exists || string(raw) == "null" {
			continue
		}
		var credentialID string
		if json.Unmarshal(raw, &credentialID) != nil {
			return "", ErrInvalidInput
		}
		tool["credential_id"] = json.RawMessage("null")
		if !c.dependencies {
			continue
		}
		credential, err := parseID(credentialID)
		if err != nil {
			return "", ErrNotFound
		}
		credentialID = copyID(credential)
		original, err := c.q.GetAdminCopyCredential(ctx, sqlc.GetAdminCopyCredentialParams{TenantID: c.source, ID: credential})
		if err != nil {
			return "", err
		}
		if _, err := c.copy(ctx, "vault", copyID(original.VaultID), ""); err != nil {
			return "", err
		}
		if target := c.mapped["credential:"+credentialID]; target != "" {
			tool["credential_id"], err = json.Marshal(target)
			if err != nil {
				return "", err
			}
		}
	}
	if _, ok := configuration["tools"]; ok {
		configuration["tools"], err = json.Marshal(tools)
		if err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(configuration)
	if err != nil {
		return "", err
	}
	target := copyUUID()
	_, err = c.q.CreateAgent(ctx, sqlc.CreateAgentParams{ID: target, TenantID: c.target, Metadata: row.Metadata, Configuration: raw})
	if err != nil {
		return "", err
	}
	model, err := c.q.GetAgentForSession(ctx, sqlc.GetAgentForSessionParams{TenantID: c.source, AgentID: source})
	if err != nil {
		return "", err
	}
	if len(model.EncryptedConfig) > 0 {
		plain, err := c.s.credentialCipher.OpenAgentModelExecution(model.EncryptedConfig, c.sourceTenant, id)
		if err != nil {
			return "", err
		}
		encrypted, err := c.s.credentialCipher.SealAgentModelExecution(plain, c.targetTenant, copyID(target))
		if err != nil {
			return "", err
		}
		if err := c.q.SaveAgentModelExecution(ctx, sqlc.SaveAgentModelExecutionParams{AgentID: target, EncryptedConfig: encrypted}); err != nil {
			return "", err
		}
	}
	return c.add("agent", id, copyID(target), ""), nil
}
