package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestProjectAPIKeysPublicAuthenticationSurvivesRestartAndRejectsRevocationOrRebind(t *testing.T) {
	st, pool := store.NewTestStore(t)
	parentToken, adminToken := uuid.NewString(), uuid.NewString()
	parent := api.APIKey{TokenSHA256: device.HashCredential(parentToken), TenantID: uuid.NewString(),
		OrganizationID: "org-" + uuid.NewString(), ProjectID: "project-" + uuid.NewString(), SubjectKind: "service_account", SubjectID: "console"}
	newHandler := func(st *store.Store, binding api.APIKey) http.Handler {
		t.Helper()
		auth, err := api.NewAuthenticator([]api.APIKey{binding})
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
		return h
	}
	call := func(h http.Handler, method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s returned %d; expected %d", method, path, w.Code, status)
		}
		return w
	}
	h := newHandler(st, parent)
	base := "/core/v1/project-api-keys/" + parent.TokenSHA256
	id := uuid.NewString()
	create := `{"id":"` + id + `","name":"Terminal"}`
	w := call(h, "POST", base, adminToken, create, http.StatusCreated)
	var issued store.IssuedProjectAPIKey
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil || issued.ID != id || !strings.HasPrefix(issued.Key, "pc_") {
		t.Fatal("create did not return the new key")
	}
	call(h, "GET", "/v1/agents", issued.Key, "", http.StatusOK)
	call(h, "POST", base, adminToken, create, http.StatusConflict)
	call(h, "GET", base, issued.Key, "", http.StatusUnauthorized)
	listed := call(h, "GET", base, adminToken, "", http.StatusOK)
	for _, private := range []string{issued.Key, device.HashCredential(issued.Key), parent.TokenSHA256, `"key"`} {
		if strings.Contains(listed.Body.String(), private) {
			t.Fatal("list exposed secret or binding material")
		}
	}
	pool.Close()
	restarted, _ := store.NewTestStore(t)
	h = newHandler(restarted, parent)
	call(h, "GET", "/v1/agents", issued.Key, "", http.StatusOK)
	rebound := parent
	rebound.SubjectID = "other-administrator"
	call(newHandler(restarted, rebound), "GET", "/v1/agents", issued.Key, "", http.StatusUnauthorized)
	replacement := parent
	replacement.TokenSHA256 = device.HashCredential(uuid.NewString())
	call(newHandler(restarted, replacement), "GET", "/v1/agents", issued.Key, "", http.StatusUnauthorized)
	call(h, "DELETE", base+"/"+id, adminToken, "", http.StatusOK)
	call(h, "GET", "/v1/agents", issued.Key, "", http.StatusUnauthorized)
	call(h, "GET", "/v1/agents", parentToken, "", http.StatusOK)
}
