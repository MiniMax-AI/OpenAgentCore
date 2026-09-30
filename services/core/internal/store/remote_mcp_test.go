package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSelfHostedServiceMCPRejectedWithoutWrites(t *testing.T) {
	s, pool, tenant, vault, credential := selfHostedMCPAdmissionFixture(t)
	handler := selfHostedMCPAdmissionHandler(t, s, tenant)

	for _, mode := range []string{"unattached", "missing", "wrong URL", "foreign Vault", "anonymous", "implicit", "explicit", "required anonymous", "required bearer"} {
		for _, initial := range []bool{false, true} {
			tool := map[string]any{"type": "mcp", "server_label": "tools", "connection_origin": "service", "transport": map[string]string{"type": "http", "server_url": "https://tools.example/mcp"}}
			if mode != "implicit" && mode != "anonymous" && mode != "required anonymous" {
				tool["credential_id"] = credential.ID
			}
			tool["required"] = strings.HasPrefix(mode, "required")
			body := map[string]any{"agent": map[string]any{"model": "model", "tools": []any{tool}}, "environment": map[string]string{"type": "self_hosted", "workspace_directory": "/workspace"}, "vault_ids": []string{vault.ID},
				"x_agents_core": map[string]any{"model_provider": store.FixtureModelProvider("codex")}}
			switch mode {
			case "anonymous", "required anonymous", "unattached":
				body["vault_ids"] = []string{}
			case "missing":
				tool["credential_id"] = uuid.NewString()
			case "wrong URL":
				tool["transport"] = map[string]string{"type": "http", "server_url": "https://other.example/mcp"}
			case "foreign Vault":
				body["vault_ids"] = []string{uuid.NewString()}
			}
			if initial {
				body["input"] = "Do not run"
			}
			raw, _ := json.Marshal(body)
			request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(string(raw)))
			request.Header.Set("Authorization", "Bearer test-token")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			// Credential selection errors (MV-03) precede the placement rejection.
			expected, message := http.StatusBadRequest, "environment:none"
			switch mode {
			case "unattached":
				message = "MCP credential_id requires an attached vault"
			case "missing":
				message = "MCP credential_id " + tool["credential_id"].(string) + " was not found in an attached vault"
			case "wrong URL":
				message = "MCP credential_id " + credential.ID + " does not match server_url https://other.example/mcp"
			case "foreign Vault":
				expected, message = http.StatusNotFound, "Resource not found."
			}
			if response.Code != expected || !strings.Contains(response.Body.String(), message) {
				t.Fatal("self-hosted service MCP admitted or wrong error", mode, response.Code, response.Body)
			}
			if strings.Contains(response.Body.String(), "synthetic-token") || strings.Contains(response.Body.String(), "ciphertext") || strings.Contains(response.Body.String(), "mcp_credentials") {
				t.Fatal("rejected request exposed private authentication")
			}

			assertSelfHostedMCPRejectionHasNoWrites(t, pool, tenant)
		}
	}
}

func selfHostedMCPAdmissionFixture(t *testing.T) (*store.Store, *pgxpool.Pool, string, store.Vault, store.Credential) {
	t.Helper()
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	tenant := uuid.NewString()
	vault, err := s.CreateVault(t.Context(), tenant, store.CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, store.CreateStaticCredentialInput{Name: "test", MCPServerURL: "https://tools.example/mcp", Token: "synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	return s, pool, tenant, vault, credential
}

func selfHostedMCPAdmissionHandler(t *testing.T, s *store.Store, tenant string) http.Handler {
	t.Helper()
	auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "test", TenantID: tenant, TokenSHA256: runtimedevice.HashCredential("test-token")}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex", api.WithEnvironmentRemoteURL("https://executor.example"))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertSelfHostedMCPRejectionHasNoWrites(t *testing.T, pool *pgxpool.Pool, tenant string) {
	t.Helper()

	for _, table := range []string{"sessions", "environments", "turns", "turn_inputs", "session_items", "session_events", "environment_input_reservations"} {
		var count int
		where := "session_id IN (SELECT id FROM sessions WHERE tenant_id=$1)"
		if table == "sessions" {
			where = "tenant_id=$1"
		}
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE "+where, tenant).Scan(&count); err != nil || count != 0 {
			t.Fatal("rejected request wrote execution state", table, count, err)
		}
	}
}
