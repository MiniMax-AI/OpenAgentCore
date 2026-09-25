package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// Deployment defaults live in Core, are managed with the Core key and are
// frozen only into hosted Sessions; self-hosted Sessions bring their own
// provider, and a Session with no provider is rejected before any write.
func TestDeploymentModelProvidersHTTP(t *testing.T) {
	_, pool := store.NewTestStore(t)
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployment_model_providers"); err != nil {
		t.Fatal(err)
	}
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{53}, 32))
	st := store.NewWithCredentialCipher(pool, cipher)
	tenant, projectKey, coreKey := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "defaults-http", TokenSHA256: device.HashCredential(projectKey), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := api.NewDeploymentAuthenticator([]string{device.HashCredential(coreKey)})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(st, auth, "codex", api.WithProjectAPIKeys(st, admin), api.WithHarnesses([]string{"codex", "mcode"}),
		api.WithHostedEnvironments(), api.WithExecution(st), api.WithEnvironmentRemoteURL("wss://core.example/api/v1/agent-daemon/ws"),
		api.WithModelProviderDefaults(st.DeploymentModelProvider))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, key, body string, status int) map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		for _, secret := range []string{"deployment-canary", "session-canary", "agent-canary", "invalid-canary", `"api_key":`} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("%s %s returned a key", method, path)
			}
		}
		if w.Code != status {
			t.Fatalf("%s %s: status %d, expected %d: %s", method, path, w.Code, status, w.Body)
		}
		var result map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	text := func(raw json.RawMessage) string {
		var value string
		_ = json.Unmarshal(raw, &value)
		return value
	}
	providerOf := func(sessionID string) string {
		t.Helper()
		provider, err := st.SessionModelExecution(t.Context(), tenant, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		return provider.APIKey
	}
	const path = "/core/v1/harnesses/codex/model-provider"
	codexDefault := `{"protocol":"responses","base_url":"https://deployment.example/v1","api_key":"deployment-canary"}`

	// Only the Core key manages defaults.
	call("GET", "/core/v1/harnesses", projectKey, "", 401)
	call("PUT", path, projectKey, codexDefault, 401)
	list := call("GET", "/core/v1/harnesses", coreKey, "", 200)
	if string(list["object"]) != `"list"` || !strings.Contains(string(list["data"]), `{"object":"core.harness","id":"claude_sdk","enabled":false,"default":false,"model_provider":null}`) ||
		!strings.Contains(string(list["data"]), `{"object":"core.harness","id":"codex","enabled":true,"default":true,"model_provider":null}`) {
		t.Fatalf("unexpected harness list: %s", list["data"])
	}
	call("GET", path, coreKey, "", 404)
	call("PUT", "/core/v1/harnesses/other/model-provider", coreKey, codexDefault, 404)
	for _, invalid := range []string{
		`{"protocol":"responses","base_url":"http://deployment.example/v1","api_key":"invalid-canary"}`,
		`{"protocol":"anthropic","base_url":"https://deployment.example/v1","api_key":"invalid-canary"}`,
		`{"protocol":"responses","base_url":"https://deployment.example/v1"}`,
		`{"protocol":"responses","base_url":"https://deployment.example/v1","api_key":"invalid-canary","api_key_configured":true}`,
		`{"protocol":"responses","base_url":"https://deployment.example/v1","api_key":"invalid-canary","context_window":-1}`,
	} {
		call("PUT", path, coreKey, invalid, 400)
	}
	call("PUT", "/core/v1/harnesses/mcode/model-provider", coreKey, `{"protocol":"anthropic","base_url":"https://deployment.example/anthropic","api_key":"invalid-canary"}`, 400)

	// Without any provider, hosted and self-hosted creation fail before any write.
	hosted := `{"agent":{"model":"hosted-model"},"environment":{"type":"openai_hosted"}}`
	selfHosted := `{"agent":{"model":"self-hosted-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`
	for _, body := range []string{hosted, selfHosted} {
		failure := call("POST", "/v1/agents/sessions", projectKey, body, 400)
		if !strings.Contains(string(failure["error"]), `"code":"model_provider_required","param":"x_agents_core.model_provider"`) {
			t.Fatalf("unclear failure: %s", failure["error"])
		}
	}

	saved := call("PUT", path, coreKey, codexDefault, 200)
	if text(saved["object"]) != "core.model_provider" || text(saved["harness"]) != "codex" || text(saved["base_url"]) != "https://deployment.example/v1" || string(saved["api_key_configured"]) != "true" || text(saved["updated_at"]) == "" {
		t.Fatalf("unexpected provider view: %v", saved)
	}
	if retrieved := call("GET", path, coreKey, "", 200); text(retrieved["base_url"]) != "https://deployment.example/v1" {
		t.Fatal("retrieved view differs")
	}

	// Hosted Sessions freeze the default; later edits never reach them.
	hostedID := text(call("POST", "/v1/agents/sessions", projectKey, hosted, 201)["id"])
	if providerOf(hostedID) != "deployment-canary" {
		t.Fatal("hosted Session did not freeze the deployment default")
	}
	projection, err := st.GetSessionExecutionConfiguration(t.Context(), tenant, hostedID)
	if err != nil || projection.ModelProvider.Source != "deployment" || projection.ModelProvider.Status != "available" || projection.ModelProvider.Configuration == nil || projection.ModelProvider.Configuration.BaseURL != "https://deployment.example/v1" {
		t.Fatal("deployment selection not recorded", projection, err)
	}
	call("PUT", path, coreKey, strings.Replace(codexDefault, "deployment.example", "changed.example", 1), 200)
	if providerOf(hostedID) != "deployment-canary" {
		t.Fatal("a changed default reached an existing Session")
	}

	// Self-hosted Sessions take the request or saved Agent bundle, never the default.
	failure := call("POST", "/v1/agents/sessions", projectKey, selfHosted, 400)
	if !strings.Contains(string(failure["error"]), "apply only to openai_hosted") {
		t.Fatalf("self-hosted Session used or misreported the deployment default: %s", failure["error"])
	}
	requestProvider := strings.TrimSuffix(selfHosted, "}") + `,"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://session.example/v1","api_key":"session-canary"}}}`
	if id := text(call("POST", "/v1/agents/sessions", projectKey, requestProvider, 201)["id"]); providerOf(id) != "session-canary" {
		t.Fatal("self-hosted Session lost its request provider")
	}
	agentID := text(call("POST", "/v1/agents", projectKey, `{"model":"agent-model","x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://agent.example/v1","api_key":"agent-canary"}}}`, 201)["id"])
	fromAgent := `{"agent_id":"` + agentID + `","environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`
	if id := text(call("POST", "/v1/agents/sessions", projectKey, fromAgent, 201)["id"]); providerOf(id) != "agent-canary" {
		t.Fatal("self-hosted Session lost its saved Agent provider")
	}

	// Removal is idempotent and audited; hosted creation then fails fast again.
	call("DELETE", path, coreKey, "", 204)
	call("DELETE", path, coreKey, "", 204)
	call("GET", path, coreKey, "", 404)
	call("POST", "/v1/agents/sessions", projectKey, hosted, 400)
	if providerOf(hostedID) != "deployment-canary" {
		t.Fatal("removing the default changed an existing Session")
	}
	page, err := st.ListAdminAudit(t.Context(), store.AdminAuditFilter{ResourceType: "deployment_model_provider", ResourceID: "codex"})
	if err != nil || len(page.Data) < 4 || page.Data[0].Action != "delete" || page.Data[0].ProjectID != nil {
		t.Fatal("deployment writes not audited", page, err)
	}
}

// A hosted or self-hosted Session created before providers were required has
// no frozen provider: new work is rejected before anything is queued.
func TestLegacySessionWithoutProviderCannotStartWork(t *testing.T) {
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), true)
	legacy, err := h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := execution.StartWorker(t.Context(), h.d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = worker.Run(ctx)
	})
	message := []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"start"}`)}}
	if _, err := worker.SubmitInputs(t.Context(), h.tenant, legacy.ID, uuid.NewString(), message); !errors.Is(err, store.ErrModelProviderRequired) {
		t.Fatal("provider-free Session accepted work", err)
	}
	_, pool := store.NewTestStore(t)
	var reservations int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1", legacy.ID).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatal("rejected work was queued", reservations, err)
	}
}
