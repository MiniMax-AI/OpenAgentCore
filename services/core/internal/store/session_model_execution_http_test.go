package store_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestModelExecutionHTTPWriteOnlyAndStrictAdmission(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{6}, 32))
	st := store.NewWithCredentialCipher(pool, cipher)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "catalog-test", TokenSHA256: device.HashCredential(token), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(st, auth, "codex", api.WithHostedEnvironments(), api.WithExecution(st))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "model-http-canary") {
			t.Fatal("credential echoed in public response")
		}
		return w
	}
	body := `{"agent":{"model":"actual-model","x_agents_core":{"harness":"codex"}},"environment":{"type":"openai_hosted"},"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://example.com/v1","api_key":"model-http-canary"}}}`
	key := uuid.NewString()
	w := call("POST", "/v1/agents/sessions", body, key)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var session struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatal("missing Session", err)
	}
	if w := call("GET", "/v1/agents/sessions/"+session.ID, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/v1/agents/sessions", body, key); w.Code != 201 {
		t.Fatal("creation retry failed", w.Code)
	}
	if w := call("POST", "/v1/agents/sessions", strings.Replace(body, "model-http-canary", "changed-key", 1), key); w.Code != 409 {
		t.Fatal("conflicting credentials accepted", w.Code)
	}
	for _, protocol := range []string{"anthropic", "chat_completions"} {
		crossProtocol := strings.Replace(body, `"protocol":"responses"`, `"protocol":"`+protocol+`"`, 1)
		response := call("POST", "/v1/agents/sessions", crossProtocol, uuid.NewString())
		if response.Code != 400 || !strings.Contains(response.Body.String(), "does not support this model provider protocol") {
			t.Fatalf("non-native Session was not rejected explicitly: %d", response.Code)
		}
	}
	for _, invalid := range []string{strings.Replace(body, `"protocol":"responses"`, `"protocol":"unknown"`, 1), strings.Replace(body, `"api_key":"model-http-canary"`, `"api_key":"model-http-canary","unknown":true`, 1), strings.Replace(body, `"type":"openai_hosted"`, `"type":"none"`, 1), strings.Replace(body, `"api_key":"model-http-canary"`, `"api_key":null`, 1)} {
		if w := call("POST", "/v1/agents/sessions", invalid, uuid.NewString()); w.Code != 400 {
			t.Fatalf("invalid execution accepted: %d %s", w.Code, w.Body)
		}
	}
}
