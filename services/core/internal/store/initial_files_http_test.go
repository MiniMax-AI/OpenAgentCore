package store_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestInitialFilesHTTPInlineLimitsAndRetry(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	tenant, token := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	// Exercise HTTP parsing and durable storage without starting a Runtime.
	handler, err := publicHandler(t, s, db, auth, "codex", storeExecution(t, s), managedSandboxes(t, s), fixtureDeploymentProvider(s))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1 << 20, 5 << 20} {
		data := bytes.Repeat([]byte{42}, size)
		files := []map[string]any{{"type": "inline", "path": "/workspace/a", "data": base64.StdEncoding.EncodeToString(data)}}
		if size == 5<<20 {
			files = append(files, map[string]any{"type": "inline", "path": "/workspace/b", "data": base64.StdEncoding.EncodeToString(data)})
		}
		body, err := json.Marshal(map[string]any{"agent": map[string]any{"model": "model"}, "environment": map[string]any{"type": "openai_hosted", "files": files}})
		if err != nil {
			t.Fatal(err)
		}
		key, id := uuid.NewString(), ""
		for range 2 {
			r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("OpenAI-Beta", "agents=v1")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", key)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("size %d: HTTP %d: %s", size, w.Code, w.Body.String())
			}
			var response struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.ID == "" || (id != "" && response.ID != id) {
				t.Fatal("creation retry did not preserve Session", err)
			}
			id = response.ID
		}
		for position := range files {
			_, actual, err := s.ReadInitialEnvironmentFile(t.Context(), tenant, id, position)
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatal("large HTTP snapshot differs", err)
			}
		}
	}
}
