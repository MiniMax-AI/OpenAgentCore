package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type executorManagementFixture struct {
	ResourceStore
	principal        identity.Principal
	environment, key string
	rotate           bool
	calls            int
	err              error
}

func (f *executorManagementFixture) IssueEnvironmentExecutorCredential(_ context.Context, principal identity.Principal, environment, key string, rotate bool) (store.IssuedExecutorCredential, error) {
	f.principal, f.environment, f.key, f.rotate = principal, environment, key, rotate
	f.calls++
	return store.IssuedExecutorCredential{KeyID: key, EnvironmentID: environment, Token: "synthetic-connect-only"}, f.err
}
func (f *executorManagementFixture) RevokeEnvironmentExecutorCredential(_ context.Context, principal identity.Principal, environment, key string) error {
	f.principal, f.environment, f.key = principal, environment, key
	f.calls++
	return f.err
}

func TestEnvironmentExecutorManagementHTTP(t *testing.T) {
	f := &executorManagementFixture{}
	tenant, environment, keyID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := NewAuthenticator([]APIKey{{TokenSHA256: device.HashCredential("caller"), TenantID: tenant, OrganizationID: "org", ProjectID: "project", SubjectKind: "user", SubjectID: "creator"}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(f, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	path := "/core/v1/environments/" + environment + "/executor-credentials"
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Tenant-ID", "untrusted")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := `{"key_id":"` + keyID + `"}`
	for _, token := range []string{"", "synthetic-connect-only", "admin"} {
		if w := request("POST", path, body, token); w.Code != 401 || f.calls != 0 {
			t.Fatal("non-caller authority accepted", w.Code)
		}
	}
	w := request("POST", path, body, "caller")
	var got map[string]string
	if w.Code != 201 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 3 || got["environment_id"] != environment || got["key_id"] != keyID || got["executor_token"] != "synthetic-connect-only" {
		t.Fatal("credential response", w.Code)
	}
	if f.principal.TenantID != tenant || f.principal.SubjectID != "creator" || f.rotate {
		t.Fatal("authority not derived from caller")
	}
	w = request("POST", path, `{"key_id":"`+keyID+`","rotate":true}`, "caller")
	if w.Code != 201 || !f.rotate {
		t.Fatal("explicit rotation", w.Code)
	}
	for _, body := range []string{`{}`, `{"key_id":"invalid"}`, `{"key_id":null}`, `{"key_id":"` + keyID + `","environment_id":"other"}`, `{"key_id":"` + keyID + `","executor_token":"import"}`, `{"key_id":"` + keyID + `","rotate":null}`} {
		before := f.calls
		if w := request("POST", path, body, "caller"); w.Code != 400 || before != f.calls {
			t.Fatal("invalid request accepted", w.Code, body)
		}
	}
	f.err = store.ErrExecutorCredentialExists
	if w := request("POST", path, body, "caller"); w.Code != 409 || strings.Contains(w.Body.String(), "synthetic-connect-only") {
		t.Fatal("uncertain retry", w.Code)
	}
	f.err = store.ErrNotFound
	if w := request("DELETE", path+"/"+keyID, "", "caller"); w.Code != 404 {
		t.Fatal("foreign revocation", w.Code)
	}
	f.err = nil
	if w := request("DELETE", path+"/"+keyID, "", "caller"); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal("revocation", w.Code)
	}
}
