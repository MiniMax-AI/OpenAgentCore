package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSelfHostedServiceMCPRejectionDoesNotRequireCredentialDecryption(t *testing.T) {
	for _, mode := range []string{"missing key", "deleted", "tampered"} {
		t.Run(mode, func(t *testing.T) {
			s, db, tenant, vault, credential := selfHostedMCPAdmissionFixture(t)
			switch mode {
			case "missing key":
				s, db = store.New(db.pool), fixtureDB{pool: db.pool}
			case "deleted":
				if _, err := s.DeleteCredential(t.Context(), tenant, vault.ID, credential.ID); err != nil {
					t.Fatal(err)
				}
			case "tampered":
				if _, err := db.pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=set_byte(token_ciphertext,15,get_byte(token_ciphertext,15) # 1) WHERE id=$1", credential.ID); err != nil {
					t.Fatal(err)
				}
			}
			handler := selfHostedMCPAdmissionHandler(t, s, db, tenant)
			for _, initial := range []bool{false, true} {
				body := map[string]any{
					"agent": map[string]any{"model": "model", "tools": []any{map[string]any{
						"type": "mcp", "server_label": "tools", "connection_origin": "service", "credential_id": credential.ID,
						"transport": map[string]string{"type": "http", "server_url": "https://tools.example/mcp"},
					}}},
					"environment":   map[string]string{"type": "self_hosted", "workspace_directory": "/workspace"},
					"vault_ids":     []string{vault.ID},
					"x_agents_core": map[string]any{"model_provider": store.FixtureModelProvider("codex")},
				}
				if initial {
					body["input"] = "must not execute"
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(string(raw)))
				request.Header.Set("Authorization", "Bearer test-token")
				request.Header.Set("OpenAI-Beta", "agents=v1")
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "synthetic-token") || strings.Contains(response.Body.String(), "ciphertext") || strings.Contains(response.Body.String(), "mcp_credentials") {
					t.Fatal("rejected MCP credential combination admitted or disclosed", response.Code, response.Body)
				}
				message := "environment:none"
				if mode == "deleted" {
					message = "MCP credential_id " + credential.ID + " was not found in an attached vault"
				}
				if !strings.Contains(response.Body.String(), message) {
					t.Fatal("unsupported placement attempted credential decryption", response.Body)
				}
				assertSelfHostedMCPRejectionHasNoWrites(t, db.pool, tenant)
			}
		})
	}
}
