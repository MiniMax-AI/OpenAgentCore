package store_test

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestUnifiedModelConfigurationHTTP(t *testing.T) {
	st, _ := store.NewManagedTestStore(t)
	tenant, token, coreKey := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "model-configuration", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential(coreKey)})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := publicHandler(t, st, auth, "codex", storeExecution(t, st), managedSandboxes(t, st), withCoreKeys(admin), withHarnesses([]string{"codex", "claude_sdk"}))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, credential, body, retry string, status int) json.RawMessage {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+credential)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		if retry != "" {
			request.Header.Set("Idempotency-Key", retry)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		for _, private := range []string{"-canary", `"api_key":`, "encrypted_config"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatal("model configuration response exposed private data")
			}
		}
		if response.Code != status {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, response.Code, status, response.Body)
		}
		return append(json.RawMessage(nil), response.Body.Bytes()...)
	}
	object := func(raw json.RawMessage) map[string]json.RawMessage {
		t.Helper()
		var result map[string]json.RawMessage
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	text := func(raw json.RawMessage) string {
		t.Helper()
		var result string
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	equalJSON := func(got json.RawMessage, want string) {
		t.Helper()
		var actual, expected any
		if json.Unmarshal(got, &actual) != nil || json.Unmarshal([]byte(want), &expected) != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("JSON mismatch: %s; want %s", got, want)
		}
	}
	const path = "/core/v1/harnesses/codex/model-configuration"
	const high = `{"model_reasoning_effort":"high"}`
	const low = `{"model_reasoning_effort":"low"}`
	const provider = `{"protocol":"responses","base_url":"https://deployment.example/v1","api_key":"deployment-canary"}`
	setDefault := func(model, native string) {
		t.Helper()
		body := `{"model":"` + model + `","harness_config":` + native + `,"model_provider":` + provider + `}`
		written := call("PUT", path, coreKey, body, "", 200)
		read := call("GET", path, coreKey, "", "", 200)
		equalJSON(read, string(written))
		view := object(read)
		if text(view["object"]) != "core.model_configuration" || text(view["model"]) != model {
			t.Fatal("model configuration identity changed")
		}
		equalJSON(view["harness_config"], native)
		equalJSON(view["model_provider"], `{"protocol":"responses","base_url":"https://deployment.example/v1","api_key_configured":true}`)
	}
	create := func(body, retry string) string {
		t.Helper()
		return text(object(call("POST", "/v1/agents/sessions", token, body, retry, 201))["id"])
	}
	assertSession := func(id, model, native, modelSource, nativeSource, providerSource, providerKey string) {
		t.Helper()
		response := object(call("GET", "/v1/agents/sessions/"+id, token, "", "", 200))
		agent := object(response["agent"])
		if text(agent["model"]) != model {
			t.Fatal("public Session model differs from its snapshot")
		}
		publicNative := json.RawMessage(`{}`)
		if extension, ok := agent["x_agents_core"]; ok && string(extension) != "null" {
			core := object(extension)
			if value, ok := core["harness_config"]; ok {
				publicNative = value
			}
		}
		equalJSON(publicNative, native)
		snapshot, err := st.GetSessionExecutionConfiguration(t.Context(), tenant, id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Model.Value == nil || *snapshot.Model.Value != model || snapshot.Model.Source != modelSource || snapshot.HarnessConfig.Source != nativeSource || snapshot.ModelProvider.Source != providerSource {
			t.Fatalf("wrong frozen selections: %#v", snapshot)
		}
		equalJSON(snapshot.HarnessConfig.Value, native)
		secret, err := st.SessionModelExecution(t.Context(), tenant, id)
		if err != nil || secret == nil || secret.APIKey != providerKey {
			t.Fatal("frozen provider changed", err)
		}
	}
	setDefault("model-original", high)
	const hosted = `{"agent":{},"environment":{"type":"openai_hosted"}}`
	retry := uuid.NewString()
	original := create(hosted, retry)
	assertSession(original, "model-original", high, "deployment", "deployment", "deployment", "deployment-canary")
	setDefault("model-replacement", low)
	if create(hosted, retry) != original {
		t.Fatal("retry resolved current defaults instead of the committed Session")
	}
	assertSession(original, "model-original", high, "deployment", "deployment", "deployment", "deployment-canary")
	fresh := create(hosted, uuid.NewString())
	if fresh == original {
		t.Fatal("new request reused the previous Session")
	}
	assertSession(fresh, "model-replacement", low, "deployment", "deployment", "deployment", "deployment-canary")

	const explicitProvider = `{"protocol":"responses","base_url":"https://explicit.example/v1","api_key":"explicit-canary"}`
	for _, tc := range []struct{ name, body, model, modelSource, providerSource, key string }{
		{"model replaces native defaults", `{"agent":{"model":"explicit-model"},"environment":{"type":"openai_hosted"}}`, "explicit-model", "session", "deployment", "deployment-canary"},
		{"provider replaces native defaults", `{"agent":{},"environment":{"type":"openai_hosted"},"x_agents_core":{"model_provider":` + explicitProvider + `}}`, "model-replacement", "deployment", "session", "explicit-canary"},
		{"explicit empty inline parameters", `{"agent":{"x_agents_core":{"harness_config":{}}},"environment":{"type":"openai_hosted"}}`, "model-replacement", "deployment", "deployment", "deployment-canary"},
		{"explicit empty Session parameters", `{"agent":{},"environment":{"type":"openai_hosted"},"x_agents_core":{"harness_config":{}}}`, "model-replacement", "deployment", "deployment", "deployment-canary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := create(tc.body, uuid.NewString())
			assertSession(id, tc.model, `{}`, tc.modelSource, "session", tc.providerSource, tc.key)
		})
	}
	const selfHosted = `{"agent":{"model":"self-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`
	failure := object(call("POST", "/v1/agents/sessions", token, selfHosted, uuid.NewString(), 400))
	if !strings.Contains(string(failure["error"]), `"code":"model_provider_required"`) {
		t.Fatal("self-hosted provider error was not explicit")
	}
	selfExplicit := strings.TrimSuffix(selfHosted, "}") + `,"x_agents_core":{"model_provider":` + explicitProvider + `}}`
	selfID := create(selfExplicit, uuid.NewString())
	assertSession(selfID, "self-model", `{}`, "session", "session", "session", "explicit-canary")
	selfWithoutModel := `{"agent":{},"environment":{"type":"self_hosted","workspace_directory":"/workspace"},"x_agents_core":{"model_provider":` + explicitProvider + `}}`
	call("POST", "/v1/agents/sessions", token, selfWithoutModel, uuid.NewString(), 400)

	// Saved Agent changes must discard parameters belonging to the former selection.
	for _, tc := range []struct{ name, update string }{
		{"model", `{"model":"another-model"}`},
		{"provider", `{"x_agents_core":{"model_provider":` + explicitProvider + `}}`},
		{"harness", `{"x_agents_core":{"harness":"claude_sdk","model_provider":` + strings.Replace(provider, `"protocol":"responses"`, `"protocol":"anthropic"`, 1) + `}}`},
	} {
		t.Run("saved Agent "+tc.name, func(t *testing.T) {
			body := `{"model":"saved-model","x_agents_core":{"harness":"codex","harness_config":` + high + `,"model_provider":` + provider + `}}`
			id := text(object(call("POST", "/v1/agents", token, body, "", 201))["id"])
			call("POST", "/v1/agents/"+id, token, tc.update, "", 200)
			saved := object(call("GET", "/v1/agents/"+id, token, "", "", 200))
			extension := object(saved["x_agents_core"])
			native, ok := extension["harness_config"]
			if !ok {
				native = json.RawMessage(`{}`)
			}
			equalJSON(native, `{}`)
			var read v1.SavedAgent
			if err := json.Unmarshal(call("GET", "/v1/agents/"+id, token, "", "", 200), &read); err != nil || read.XAgentsCore == nil || read.XAgentsCore.ModelProvider == nil || !read.XAgentsCore.ModelProvider.APIKeyConfigured {
				t.Fatal("saved update lost its safe provider", err)
			}
		})
	}
	// Disabling tools cannot enable a non-native model protocol.
	for _, protocol := range []string{"anthropic", "chat_completions"} {
		t.Run("non-native protocol "+protocol, func(t *testing.T) {
			incompatible := `{"protocol":"` + protocol + `","base_url":"https://model.example/v1","api_key":"rejected-canary"}`
			agent := `{"model":"fixed-model","tools":[{"type":"web_search","mode":"disabled"}]}`
			call("POST", "/v1/agents/sessions", token, `{"agent":`+agent+`,"environment":{"type":"openai_hosted"},"x_agents_core":{"model_provider":`+incompatible+`}}`, uuid.NewString(), 400)
			saved := strings.TrimSuffix(agent, "}") + `,"x_agents_core":{"harness":"codex","model_provider":` + incompatible + `}}`
			call("POST", "/v1/agents", token, saved, "", 400)
		})
	}
}
