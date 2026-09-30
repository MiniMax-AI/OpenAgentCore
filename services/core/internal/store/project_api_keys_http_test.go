package store_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestProjectAndSharedKeysHTTPManagement(t *testing.T) {
	st, _ := store.NewTestStore(t)
	adminToken := uuid.NewString()
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential(adminToken)})
	if err != nil {
		t.Fatal(err)
	}
	h, err := publicHandler(t, st, nil, "codex", storeKeys(st), withCoreKeys(admin))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s got%d want%d", method, path, w.Code, status)
		}
		return w
	}
	base := "/core/v1/projects"
	response := call("POST", base, adminToken, `{"name":"Default"}`, 201)
	var p store.Project
	if json.Unmarshal(response.Body.Bytes(), &p) != nil || p.ID == "" {
		t.Fatal("Project response invalid")
	}
	keysPath := base + "/" + p.ID + "/keys"
	var first, second store.IssuedProjectAPIKey
	if json.Unmarshal(call("POST", keysPath, adminToken, `{"name":"first"}`, 201).Body.Bytes(), &first) != nil {
		t.Fatal("key response invalid")
	}
	_ = json.Unmarshal(call("POST", keysPath, adminToken, `{"name":"second"}`, 201).Body.Bytes(), &second)
	call("GET", "/v1/agents", first.Key, "", 200)
	call("GET", "/v1/agents", second.Key, "", 200)
	call("GET", "/v1/agents", adminToken, "", 401)
	call("GET", base, first.Key, "", 401)
	list := call("GET", keysPath, adminToken, "", 200)
	if strings.Contains(list.Body.String(), first.Key) {
		t.Fatal("list exposed plaintext")
	}
	call("POST", base+"/"+p.ID, adminToken, `{"name":"renamed"}`, 200)
	call("GET", "/v1/agents", first.Key, "", 200)
	call("DELETE", keysPath+"/"+first.ID, adminToken, "", 200)
	call("GET", "/v1/agents", first.Key, "", 401)
	call("GET", "/v1/agents", second.Key, "", 200)
	call("POST", base+"/"+p.ID+"/archive", adminToken, "", 200)
	call("GET", "/v1/agents", second.Key, "", 401)
	call("GET", keysPath, adminToken, "", 200)
	call("POST", keysPath, adminToken, `{"name":"forbidden"}`, 409)
	call("POST", base, adminToken, `{"id":"`+uuid.NewString()+`","name":"forged"}`, 400)
}
