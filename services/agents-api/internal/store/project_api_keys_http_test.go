package store_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestProjectAPIKeysHTTPIndependentLifecycle(t *testing.T) {
	st, _ := store.NewTestStore(t)
	adminToken := uuid.NewString()
	auth, err := api.NewAuthenticator(nil)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := api.NewDeploymentAuthenticator([]string{device.HashCredential(adminToken)})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(st, auth, "codex", api.WithProjectAPIKeys(st, admin))
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
			t.Fatalf("%s %s: got%d want%d", method, path, w.Code, status)
		}
		return w
	}
	base := "/core/v1/admin/api-keys"
	id := uuid.NewString()
	response := call("POST", base, adminToken, `{"id":"`+id+`","name":"terminal"}`, 201)
	var issued store.IssuedProjectAPIKey
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	call("POST", base, adminToken, `{"id":"`+id+`","name":"duplicate"}`, 409)
	call("GET", "/v1/agents", issued.Key, "", 200)
	call("GET", "/v1/agents", adminToken, "", 401)
	call("GET", base, issued.Key, "", 401)
	list := call("GET", base, adminToken, "", 200)
	if strings.Contains(list.Body.String(), issued.Key) {
		t.Fatal("list leaked secret")
	}
	resetResponse := call("POST", base+"/"+id+"/reset", adminToken, `{"request_id":"`+uuid.NewString()+`"}`, 200)
	var reset store.IssuedProjectAPIKey
	_ = json.Unmarshal(resetResponse.Body.Bytes(), &reset)
	call("GET", "/v1/agents", issued.Key, "", 401)
	call("GET", "/v1/agents", reset.Key, "", 200)
	call("DELETE", base+"/"+id, adminToken, "", 200)
	call("GET", "/v1/agents", reset.Key, "", 401)
	call("GET", base+"/"+id, adminToken, "", 200)
}
