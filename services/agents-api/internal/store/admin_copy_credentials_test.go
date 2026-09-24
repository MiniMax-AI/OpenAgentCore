package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

func TestAdminCopyAgentVaultCredentialsAndEncryptedProvider(t *testing.T) {
	s, source, target := adminCopyFixture(t)
	vault, err := s.CreateVault(t.Context(), source, CreateVaultInput{Metadata: map[string]string{"purpose": "copy"}})
	if err != nil {
		t.Fatal(err)
	}
	static, err := s.CreateStaticCredential(t.Context(), source, vault.ID, CreateStaticCredentialInput{Name: "static", MCPServerURL: "https://copy.example/mcp", Token: "copy-static-secret"})
	if err != nil {
		t.Fatal(err)
	}
	oauth, err := s.CreateOAuthCredential(t.Context(), source, vault.ID, CreateOAuthCredentialInput{Name: "oauth", MCPServerURL: "https://copy.example/oauth", AccessToken: "copy-oauth-secret"})
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := s.CreateOAuthCredential(t.Context(), source, vault.ID, CreateOAuthCredentialInput{Name: "refresh", MCPServerURL: "https://copy.example/refresh", AccessToken: "copy-access-secret", RefreshToken: "copy-refresh-secret", OAuth: OAuthMetadata{Refresh: &OAuthRefreshMetadata{ClientID: "copy-client", TokenEndpoint: "https://copy.example/token", TokenEndpointAuth: "none"}}})
	if err != nil {
		t.Fatal(err)
	}
	provider := agentProviderFixture(66)
	var configuration map[string]any
	if err := json.Unmarshal(agentProviderConfiguration(t, provider, "codex"), &configuration); err != nil {
		t.Fatal(err)
	}
	configuration["tools"] = []map[string]any{{"type": "mcp", "server_label": "static", "credential_id": strings.ToUpper(static.ID)}, {"type": "mcp", "server_label": "refresh", "credential_id": refresh.ID}}
	raw, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(t.Context(), source, CreateAgentInput{Configuration: raw, ModelProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.CopyAssets(adminCopyContext(t, target), source, target, CopyAssetsInput{ResourceType: "agent", ResourceID: strings.ToUpper(agent.ID), IncludeDependencies: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].SourceID != refresh.ID {
		t.Fatal("refresh grant not skipped")
	}
	copyAgent := copiedID(t, result, "agent", agent.ID)
	copyVault := copiedID(t, result, "vault", vault.ID)
	copyStatic := copiedID(t, result, "credential", static.ID)
	copyOAuth := copiedID(t, result, "credential", oauth.ID)
	saved, inherited, err := s.GetAgentForSession(t.Context(), target, copyAgent, true)
	if err != nil || inherited == nil || *inherited != *provider {
		t.Fatal("provider not rebound", err)
	}
	var copied struct {
		Tools []struct {
			CredentialID *string `json:"credential_id"`
		} `json:"tools"`
	}
	if json.Unmarshal(saved.Configuration, &copied) != nil || len(copied.Tools) != 2 || copied.Tools[0].CredentialID == nil || *copied.Tools[0].CredentialID != copyStatic || copied.Tools[1].CredentialID != nil {
		t.Fatal("agent credentials not remapped or cleared")
	}
	for _, pair := range []struct{ id, kind, token string }{{copyStatic, "static_bearer", "copy-static-secret"}, {copyOAuth, "mcp_oauth", "copy-oauth-secret"}} {
		tenantID, _ := parseID(target)
		credentialID, _ := parseID(pair.id)
		row, err := s.queries.GetAdminCopyCredential(t.Context(), sqlc.GetAdminCopyCredentialParams{TenantID: tenantID, ID: credentialID})
		if err != nil {
			t.Fatal(err)
		}
		binding := credentialcrypto.Binding{TenantID: target, VaultID: copyVault, CredentialID: pair.id, AuthType: pair.kind, Destination: row.McpServerUrl}
		plain, err := s.credentialCipher.Open(row.TokenCiphertext, binding)
		if err != nil || !bytes.Contains(plain, []byte(pair.token)) {
			t.Fatal("credential not rebound", err)
		}
		binding.TenantID = source
		if _, err := s.credentialCipher.Open(row.TokenCiphertext, binding); err == nil {
			t.Fatal("old tenant decrypted copied grant")
		}
	}
	without, err := s.CopyAssets(adminCopyContext(t, target), source, target, CopyAssetsInput{ResourceType: "agent", ResourceID: agent.ID})
	if err != nil || len(without.Mappings) != 1 {
		t.Fatal("dependency-disabled agent copy", err)
	}
	cleared, err := s.GetAgent(t.Context(), target, copiedID(t, without, "agent", agent.ID))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(cleared.Configuration, &copied) != nil || copied.Tools[0].CredentialID != nil || copied.Tools[1].CredentialID != nil {
		t.Fatal("dependency-disabled credential retained")
	}
	standalone, err := s.CopyAssets(adminCopyContext(t, target), source, target, CopyAssetsInput{ResourceType: "credential", ResourceID: static.ID, TargetVaultID: copyVault})
	if err != nil || len(standalone.Mappings) != 1 {
		t.Fatal("standalone credential copy", err)
	}
	if _, err := s.CopyAssets(adminCopyContext(t, target), source, target, CopyAssetsInput{ResourceType: "credential", ResourceID: static.ID, TargetVaultID: vault.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign target vault accepted", err)
	}
	// Copying a Vault directly includes every grant, independently of the optional
	// dependency flag used by Agents and Templates.
	direct, err := s.CopyAssets(adminCopyContext(t, target), source, target, CopyAssetsInput{ResourceType: "vault", ResourceID: vault.ID})
	if err != nil || len(direct.Mappings) != 3 || len(direct.Skipped) != 1 {
		t.Fatal("direct vault copy incomplete", err)
	}
	raw, _ = json.Marshal(result)
	for _, secret := range []string{provider.APIKey, "copy-static-secret", "copy-oauth-secret", "copy-refresh-secret", "copy-access-secret"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("copy response exposes secret")
		}
		var count int
		if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM admin_audit_log WHERE tenant_id=$1 AND result_ids::text LIKE '%' || $2 || '%'", target, secret).Scan(&count); err != nil || count != 0 {
			t.Fatal("audit exposes secret", err)
		}
	}
}

func TestAdminCopyCredentialCanonicalBindings(t *testing.T) {
	for _, kind := range []string{"static_bearer", "mcp_oauth"} {
		t.Run(kind, func(t *testing.T) {
			s, source, target := adminCopyFixture(t)
			vault, err := s.CreateVault(t.Context(), source, CreateVaultInput{})
			if err != nil {
				t.Fatal(err)
			}
			destination, err := s.CreateVault(t.Context(), target, CreateVaultInput{})
			if err != nil {
				t.Fatal(err)
			}
			const token = "canonical-copy-private-token"
			const url = "https://canonical-copy.example/mcp"
			var credential Credential
			if kind == "static_bearer" {
				credential, err = s.CreateStaticCredential(t.Context(), source, vault.ID, CreateStaticCredentialInput{Name: "canonical", MCPServerURL: url, Token: token})
			} else {
				credential, err = s.CreateOAuthCredential(t.Context(), source, vault.ID, CreateOAuthCredentialInput{Name: "canonical", MCPServerURL: url, AccessToken: token})
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, sourceID := range []string{credential.ID, strings.ToUpper(credential.ID)} {
				result, err := s.CopyAssets(adminCopyContext(t, target), source, target, CopyAssetsInput{ResourceType: "credential", ResourceID: sourceID, TargetVaultID: strings.ToUpper(destination.ID)})
				if err != nil {
					t.Fatal("valid UUID copy failed", err)
				}
				id := copiedID(t, result, "credential", credential.ID)
				selected, err := s.ResolveMCPCredentials(t.Context(), target, []string{destination.ID}, []MCPCredentialRequest{{ServerLabel: "copy", ServerURL: url, CredentialID: &id}})
				if err != nil || len(selected) != 1 {
					t.Fatal("copied credential cannot be selected", err)
				}
				got, err := s.MCPBearerToken(t.Context(), target, []string{destination.ID}, selected[0])
				if err != nil || got != token {
					t.Fatal("committed copy cannot decrypt through normal execution lookup", err)
				}
			}
		})
	}
}
