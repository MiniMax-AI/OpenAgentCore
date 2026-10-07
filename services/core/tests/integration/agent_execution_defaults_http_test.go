package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func TestAgentExecutionDefaultsPublicSnapshotAndPrecedence(t *testing.T) {
	_, pool := testStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{19}, 32))
	st := NewWithCredentialCipher(pool, cipher)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "defaults-test", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	deployment := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://deployment.example/v1", APIKey: "deployment-canary"}
	defaultsCalls := 0
	handler, err := publicHandler(t, st, auth, "codex", storeExecution(t, st), managedSandboxes(t, st), withHarnesses([]string{"codex", "claude_sdk", "mcode"}), modelProviderDefaults(func(context.Context, string) (*modelconfiguration.Snapshot, error) {
		defaultsCalls++
		copy := *deployment
		return &modelconfiguration.Snapshot{Model: "fixture", Provider: &copy, Revision: uuid.New()}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, key string, status int) map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		for _, forbidden := range []string{"saved-canary", "override-canary", "deployment-canary", `"api_key":`, "encrypted_config"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatal("provider secret leaked")
			}
		}
		if w.Code != status {
			t.Fatalf("%s %s: status %d, expected %d: %s", method, path, w.Code, status, w.Body)
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("invalid response")
		}
		return result
	}
	id := func(result map[string]json.RawMessage) string {
		var s string
		_ = json.Unmarshal(result["id"], &s)
		return s
	}
	createAgent := `{"model":"model-original","x_agents_core":{"harness":"codex","model_provider":{"protocol":"responses","base_url":"https://saved.example/v1","api_key":"saved-canary"}}}`
	agent := call("POST", "/v1/agents", createAgent, "", 201)
	agentID := id(agent)
	if !bytes.Contains(agent["x_agents_core"], []byte(`"api_key_configured":true`)) {
		t.Fatal("missing safe configured flag")
	}
	call("GET", "/v1/agents/"+agentID, "", "", 200)
	call("GET", "/v1/agents", "", "", 200)
	body := `{"agent_id":"` + agentID + `","environment":{"type":"openai_hosted"}}`
	key := uuid.NewString()
	sessionID := id(call("POST", "/v1/agents/sessions", body, key, 201))
	assertSnapshot := func(sessionID, model, endpoint, key string) {
		t.Helper()
		session, err := sessionAdapter(st).GetSession(t.Context(), tenant, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct{ Agent struct{ Model string } }
		if json.Unmarshal(session.Configuration, &cfg) != nil || cfg.Agent.Model != model {
			t.Fatal("model snapshot changed")
		}
		provider, err := sessionAdapter(st).SessionModelExecution(t.Context(), tenant, sessionID)
		if err != nil || provider.BaseURL != endpoint || provider.APIKey != key {
			t.Fatal("provider snapshot mismatch", err)
		}
	}
	assertSnapshot(sessionID, "model-original", "https://saved.example/v1", "saved-canary")
	modelOnly := `{"agent_id":"` + agentID + `","agent":{"model":"model-override"},"environment":{"type":"openai_hosted"}}`
	sid := id(call("POST", "/v1/agents/sessions", modelOnly, uuid.NewString(), 201))
	assertSnapshot(sid, "model-override", "https://saved.example/v1", "saved-canary")
	replacement := `{"agent_id":"` + agentID + `","environment":{"type":"openai_hosted"},"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://override.example/api","api_key":"override-canary"}}}`
	sid = id(call("POST", "/v1/agents/sessions", replacement, uuid.NewString(), 201))
	assertSnapshot(sid, "model-original", "https://override.example/api", "override-canary")
	for _, protocol := range []string{"anthropic", "chat_completions"} {
		crossProtocol := strings.Replace(replacement, `"protocol":"responses"`, `"protocol":"`+protocol+`"`, 1)
		call("POST", "/v1/agents/sessions", crossProtocol, uuid.NewString(), 400)
	}
	crossHarness := strings.Replace(modelOnly, `"model":"model-override"`, `"model":"model-override","x_agents_core":{"harness":"claude_sdk"}`, 1)
	call("POST", "/v1/agents/sessions", crossHarness, uuid.NewString(), 400)
	assertSnapshot(sessionID, "model-original", "https://saved.example/v1", "saved-canary")
	// Switching to another harness requires a complete native provider bundle.
	nativeHarness := strings.TrimSuffix(crossHarness, "}") + `,"x_agents_core":{"model_provider":{"protocol":"anthropic","base_url":"https://override.example/api","api_key":"override-canary"}}}`
	created := id(call("POST", "/v1/agents/sessions", nativeHarness, uuid.NewString(), 201))
	assertSnapshot(created, "model-override", "https://override.example/api", "override-canary")
	reads := sessionAdapter(st)
	resolved, err := reads.GetSession(t.Context(), tenant, created)
	frozen, providerErr := reads.SessionModelExecution(t.Context(), tenant, created)
	if err != nil || providerErr != nil || resolved.Engine != "claude_sdk" || frozen == nil || frozen.Protocol != "anthropic" {
		t.Fatal("harness override did not freeze the native provider", err, providerErr)
	}
	for _, raw := range []string{
		strings.Replace(replacement, `,"api_key":"override-canary"`, "", 1),
		strings.Replace(replacement, `"protocol":"responses"`, `"protocol":"unknown"`, 1),
		strings.Replace(modelOnly, `"model":"model-override"`, `"model":"model-override","x_agents_core":{"harness":"unknown"}`, 1),
		strings.TrimSuffix(strings.Replace(body, `"type":"openai_hosted"`, `"type":"none"`, 1), "}") + `,"input":"test"}`,
	} {
		call("POST", "/v1/agents/sessions", raw, uuid.NewString(), 400)
	}
	if defaultsCalls != 0 {
		t.Fatal("explicit or inherited bundle consulted deployment")
	}
	nullProvider := strings.TrimSuffix(body, "}") + `,"x_agents_core":{"model_provider":null}}`
	sid = id(call("POST", "/v1/agents/sessions", nullProvider, uuid.NewString(), 201))
	assertSnapshot(sid, "model-original", "https://saved.example/v1", "saved-canary")
	call("POST", "/v1/agents/"+agentID, `{"model":"model-new","x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://override.example/api","api_key":"override-canary"}}}`, "", 200)
	fresh := id(call("POST", "/v1/agents/sessions", body, uuid.NewString(), 201))
	assertSnapshot(fresh, "model-new", "https://override.example/api", "override-canary")
	call("DELETE", "/v1/agents/"+agentID, "", "", 200)
	if id(call("POST", "/v1/agents/sessions", body, key, 201)) != sessionID {
		t.Fatal("retry created another Session")
	}
	call("POST", "/v1/agents/sessions", body, uuid.NewString(), 404)
	st = NewWithCredentialCipher(pool, cipher)
	assertSnapshot(sessionID, "model-original", "https://saved.example/v1", "saved-canary")
	assertSnapshot(fresh, "model-new", "https://override.example/api", "override-canary")
	inline := `{"agent":{"model":"inline-model"},"environment":{"type":"openai_hosted"}}`
	inlineKey := uuid.NewString()
	sid = id(call("POST", "/v1/agents/sessions", inline, inlineKey, 201))
	assertSnapshot(sid, "inline-model", "https://deployment.example/v1", "deployment-canary")
	deployment.BaseURL = "https://changed.example/v1"
	deployment.APIKey = "changed-key"
	if id(call("POST", "/v1/agents/sessions", inline, inlineKey, 201)) != sid || defaultsCalls != 1 {
		t.Fatal("retry re-resolved deployment defaults")
	}
	assertSnapshot(sid, "inline-model", "https://deployment.example/v1", "deployment-canary")
	newID := id(call("POST", "/v1/agents/sessions", inline, uuid.NewString(), 201))
	assertSnapshot(newID, "inline-model", "https://changed.example/v1", "changed-key")
	// Preparation alone must not require resubmitting existing model credentials.
	savedID := id(call("POST", "/v1/agents", createAgent, "", 201))
	for _, placement := range []string{"openai_hosted", "self_hosted"} {
		environment := `{"type":"` + placement + `"}`
		if placement == "self_hosted" {
			environment = `{"type":"self_hosted","workspace_directory":"/work"}`
		}
		prepared := `{"agent_id":"` + savedID + `","environment":` + environment + `,"x_agents_core":{"environment":{"env":{"PREPARATION_PROOF":"saved"}}}}`
		preparedID := id(call("POST", "/v1/agents/sessions", prepared, uuid.NewString(), 201))
		assertSnapshot(preparedID, "model-original", "https://saved.example/v1", "saved-canary")
	}
	preparedInline := strings.TrimSuffix(inline, "}") + `,"x_agents_core":{"environment":{"env":{"PREPARATION_PROOF":"deployment"}}}}`
	preparedID := id(call("POST", "/v1/agents/sessions", preparedInline, uuid.NewString(), 201))
	assertSnapshot(preparedID, "inline-model", "https://changed.example/v1", "changed-key")
	callsBefore := defaultsCalls
	selfHosted := strings.Replace(preparedInline, `"type":"openai_hosted"`, `"type":"self_hosted","workspace_directory":"/work"`, 1)
	call("POST", "/v1/agents/sessions", selfHosted, uuid.NewString(), 400)
	if defaultsCalls != callsBefore {
		t.Fatal("preparation extension sent deployment credentials to a user machine")
	}

}

// An Agent whose saved provider an earlier release accepted but validation
// now rejects still reads, and a Session that inherits the provider is
// refused with the validation error rather than an internal one.
func TestAgentExecutionDefaultsRejectInvalidSavedProvider(t *testing.T) {
	_, pool := testStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{19}, 32))
	st := NewWithCredentialCipher(pool, cipher)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "invalid-saved-provider", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	handler, err := publicHandler(t, st, auth, "claude_sdk", withHarnesses([]string{"claude_sdk"}))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", uuid.NewString())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := call("POST", "/v1/agents", `{"model":"fixture","x_agents_core":{"harness":"claude_sdk","model_provider":{"protocol":"anthropic","base_url":"https://saved.example/anthropic","api_key":"saved-canary"}}}`)
	var agent struct {
		ID string `json:"id"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &agent) != nil {
		t.Fatalf("create Agent: %d %s", w.Code, w.Body)
	}
	stale, _ := json.Marshal(v1.ModelProviderInput{Protocol: "anthropic", BaseURL: "https://saved.example/anthropic/v1", APIKey: "saved-canary"})
	sealed, err := cipher.SealAgentModelExecution(stale, tenant, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE agent_model_execution SET encrypted_config = $1 WHERE agent_id = $2", sealed, agent.ID); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/v1/agents/"+agent.ID, ""); w.Code != 200 {
		t.Fatalf("read Agent: %d %s", w.Code, w.Body)
	}
	const rejection = `{"error":{"message":"an anthropic base_url excludes the /v1 version path","type":"invalid_request_error","code":"unsupported_or_invalid_configuration","param":null}}` + "\n"
	if w := call("POST", "/v1/agents/sessions", `{"agent_id":"`+agent.ID+`","environment":{"type":"openai_hosted"}}`); w.Code != 400 || w.Body.String() != rejection {
		t.Fatalf("inherited Session: %d %s", w.Code, w.Body)
	}
}
