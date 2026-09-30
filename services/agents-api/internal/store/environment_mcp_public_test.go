package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestPublicEnvironmentMCPUsesAttachedVaultSelection(t *testing.T) {
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		t.Run(kind, func(t *testing.T) {
			s, pool, tenant, vault, credential := selfHostedMCPAdmissionFixture(t)
			auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "test", TenantID: tenant, TokenSHA256: device.HashCredential("test-token")}})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := api.NewHandler(s, auth, kind, api.WithEnvironmentRemoteURL("https://executor.example"), api.WithExecution(&execution.Worker{}))
			if err != nil {
				t.Fatal(err)
			}
			send := func(origin string, vaults []string, cred any, allowed any, required bool) *httptest.ResponseRecorder {
				tool := map[string]any{"type": "mcp", "server_label": "proof", "connection_origin": origin, "credential_id": cred, "allowed_tools": allowed, "required": required, "transport": map[string]string{"type": "http", "server_url": "https://tools.example/mcp"}}
				body := map[string]any{"agent": map[string]any{"model": "model", "tools": []any{tool}}, "environment": map[string]string{"type": "self_hosted", "workspace_directory": "/workspace"}, "vault_ids": vaults, "x_agents_core": map[string]any{"model_provider": store.FixtureModelProvider(kind)}}
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(string(raw)))
				req.Header.Set("Authorization", "Bearer test-token")
				req.Header.Set("OpenAI-Beta", "agents=v1")
				req.Header.Set("Content-Type", "application/json")
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, req)
				if strings.Contains(out.Body.String(), "synthetic-token") || strings.Contains(out.Body.String(), "ciphertext") {
					t.Fatal("credential disclosed")
				}
				return out
			}
			for _, r := range []*httptest.ResponseRecorder{send("service", []string{vault.ID}, credential.ID, nil, false), send("environment", []string{}, credential.ID, nil, false)} {
				if r.Code != 400 {
					t.Fatal("unsupported origin or unattached credential admitted", r.Code, r.Body)
				}
			}
			if kind == "mcode" {
				for _, r := range []*httptest.ResponseRecorder{send("environment", []string{vault.ID}, credential.ID, []string{}, false), send("environment", []string{vault.ID}, credential.ID, nil, true)} {
					if r.Code != 400 {
						t.Fatal("unsupported native policy admitted", r.Code, r.Body)
					}
				}
			}
			assertSelfHostedMCPRejectionHasNoWrites(t, pool, tenant)
			for _, r := range []*httptest.ResponseRecorder{send("environment", []string{}, nil, nil, false), send("environment", []string{vault.ID}, credential.ID, nil, false), send("environment", []string{vault.ID}, nil, nil, false)} {
				if r.Code != 201 {
					t.Fatal("qualified public MCP rejected", r.Code, r.Body)
				}
				var session struct {
					Agent struct {
						Tools []struct {
							ConnectionOrigin string  `json:"connection_origin"`
							CredentialID     *string `json:"credential_id"`
						}
					}
				}
				if json.Unmarshal(r.Body.Bytes(), &session) != nil || len(session.Agent.Tools) != 1 || session.Agent.Tools[0].ConnectionOrigin != "environment" {
					t.Fatal("public origin not retained")
				}
			}
		})
	}
}
