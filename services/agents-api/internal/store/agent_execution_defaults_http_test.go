package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestAgentExecutionDefaultsPublicSnapshotAndPrecedence(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{19}, 32))
	st := store.NewWithCredentialCipher(pool, cipher)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "defaults-test", TokenSHA256: device.HashCredential(token), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	deployment := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://deployment.example/v1", APIKey: "deployment-canary"}
	defaultsCalls := 0
	handler, err := api.NewHandler(st, auth, "codex", api.WithHarnesses([]string{"codex", "claude_sdk", "mcode"}), api.WithHostedEnvironments(), api.WithExecution(st), api.WithModelProviderDefaults(func(context.Context, string, string) (*v1.ModelProviderInput, map[string]any, error) {
		defaultsCalls++
		copy := *deployment
		return &copy, nil, nil
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
		session, err := st.GetSession(t.Context(), tenant, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct{ Agent struct{ Model string } }
		if json.Unmarshal(session.Configuration, &cfg) != nil || cfg.Agent.Model != model {
			t.Fatal("model snapshot changed")
		}
		provider, err := st.SessionModelExecution(t.Context(), tenant, sessionID)
		if err != nil || provider.BaseURL != endpoint || provider.APIKey != key {
			t.Fatal("provider snapshot mismatch", err)
		}
	}
	assertSnapshot(sessionID, "model-original", "https://saved.example/v1", "saved-canary")
	modelOnly := `{"agent_id":"` + agentID + `","agent":{"model":"model-override"},"environment":{"type":"openai_hosted"}}`
	sid := id(call("POST", "/v1/agents/sessions", modelOnly, uuid.NewString(), 201))
	assertSnapshot(sid, "model-override", "https://saved.example/v1", "saved-canary")
	replacement := `{"agent_id":"` + agentID + `","environment":{"type":"openai_hosted"},"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://override.example/v1","api_key":"override-canary"}}}`
	sid = id(call("POST", "/v1/agents/sessions", replacement, uuid.NewString(), 201))
	assertSnapshot(sid, "model-original", "https://override.example/v1", "override-canary")
	for _, raw := range []string{
		strings.Replace(replacement, `,"api_key":"override-canary"`, "", 1),
		strings.Replace(replacement, `"protocol":"responses"`, `"protocol":"anthropic"`, 1),
		strings.Replace(modelOnly, `"model":"model-override"`, `"model":"model-override","x_agents_core":{"harness":"claude_sdk"}`, 1),
		strings.TrimSuffix(strings.Replace(body, `"type":"openai_hosted"`, `"type":"none"`, 1), "}") + `,"input":"test"}`,
		strings.Replace(body, `"type":"openai_hosted"`, `"type":"self_hosted","workspace_directory":"/workspace"`, 1),
	} {
		call("POST", "/v1/agents/sessions", raw, uuid.NewString(), 400)
	}
	if defaultsCalls != 0 {
		t.Fatal("explicit or inherited bundle consulted deployment")
	}
	nullProvider := strings.TrimSuffix(body, "}") + `,"x_agents_core":{"model_provider":null}}`
	sid = id(call("POST", "/v1/agents/sessions", nullProvider, uuid.NewString(), 201))
	assertSnapshot(sid, "model-original", "https://saved.example/v1", "saved-canary")
	call("POST", "/v1/agents/"+agentID, `{"model":"model-new","x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://override.example/v1","api_key":"override-canary"}}}`, "", 200)
	fresh := id(call("POST", "/v1/agents/sessions", body, uuid.NewString(), 201))
	assertSnapshot(fresh, "model-new", "https://override.example/v1", "override-canary")
	call("DELETE", "/v1/agents/"+agentID, "", "", 200)
	if id(call("POST", "/v1/agents/sessions", body, key, 201)) != sessionID {
		t.Fatal("retry created another Session")
	}
	call("POST", "/v1/agents/sessions", body, uuid.NewString(), 404)
	st = store.NewWithCredentialCipher(pool, cipher)
	assertSnapshot(sessionID, "model-original", "https://saved.example/v1", "saved-canary")
	assertSnapshot(fresh, "model-new", "https://override.example/v1", "override-canary")
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
}
