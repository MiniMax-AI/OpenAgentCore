package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type executorManagementFixture struct {
	ResourceStore
	principal        identity.Principal
	environment, key string
	rotate, audited  bool
	calls            int
	err              error
}

func (f *executorManagementFixture) record(ctx context.Context, principal identity.Principal, environment, key string) {
	f.principal, f.environment, f.key = principal, environment, key
	_, f.audited = adminaudit.FromContext(ctx)
	f.calls++
}
func (f *executorManagementFixture) ListProjectExecutorCredentials(ctx context.Context, principal identity.Principal, environment string) ([]store.ExecutorCredential, error) {
	f.record(ctx, principal, environment, "")
	return []store.ExecutorCredential{{KeyID: "listed", CreatedAt: time.Unix(1, 0).UTC()}}, f.err
}
func (f *executorManagementFixture) IssueProjectExecutorCredential(ctx context.Context, principal identity.Principal, environment, key string, rotate bool) (store.IssuedExecutorCredential, error) {
	f.record(ctx, principal, environment, key)
	f.rotate = rotate
	return store.IssuedExecutorCredential{KeyID: key, EnvironmentID: environment, Token: "synthetic-connect-only"}, f.err
}
func (f *executorManagementFixture) RevokeProjectExecutorCredential(ctx context.Context, principal identity.Principal, environment, key string) error {
	f.record(ctx, principal, environment, key)
	return f.err
}

func TestProjectExecutorCredentialsHTTP(t *testing.T) {
	f := &executorManagementFixture{}
	key := callerBinding()
	auth, err := NewAuthenticator([]APIKey{key})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewDeploymentAuthenticator([]string{device.HashCredential("admin")})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(f, auth, "codex", WithProjectAPIKeys(managementProjectStore(key), admin))
	if err != nil {
		t.Fatal(err)
	}
	environment, keyID := uuid.NewString(), uuid.NewString()
	path := "/core/v1/projects/" + managementProjectID + "/environments/" + environment + "/executor-credentials"
	body := `{"key_id":"` + keyID + `"}`
	// Only the Core key authorizes; the Project API key and the old route are refused.
	for _, token := range []string{"", "caller", "synthetic-connect-only"} {
		if w := projectKeyHTTP(h, "POST", path, token, body); w.Code != 401 || f.calls != 0 {
			t.Fatal("non-Core credential accepted", w.Code)
		}
	}
	if w := projectKeyHTTP(h, "POST", "/core/v1/environments/"+environment+"/executor-credentials", "caller", body); w.Code != 401 || f.calls != 0 {
		t.Fatal("old Project-key route", w.Code)
	}
	if w := projectKeyHTTP(h, "POST", "/core/v1/environments/"+environment+"/executor-credentials", "admin", body); w.Code != 404 || f.calls != 0 {
		t.Fatal("old route still served", w.Code)
	}
	unknown := "/core/v1/projects/" + uuid.NewString() + "/environments/" + environment + "/executor-credentials"
	if w := projectKeyHTTP(h, "GET", unknown, "admin", ""); w.Code != 404 || f.calls != 0 {
		t.Fatal("unknown Project", w.Code)
	}
	// The request body is validated before the target is resolved.
	if w := projectKeyHTTP(h, "POST", unknown, "admin", `{"key_id":"invalid"}`); w.Code != 400 || f.calls != 0 {
		t.Fatal("body before target", w.Code)
	}
	if w := projectKeyHTTP(h, "POST", unknown, "admin", body); w.Code != 404 || f.calls != 0 {
		t.Fatal("unknown Project issue", w.Code)
	}

	w := projectKeyHTTP(h, "GET", path, "admin", "")
	if w.Code != 200 || w.Body.String() != `{"data":[{"key_id":"listed","created_at":"1970-01-01T00:00:01Z","revoked_at":null}]}`+"\n" {
		t.Fatal("list", w.Code, w.Body)
	}
	w = projectKeyHTTP(h, "POST", path, "admin", body)
	var got map[string]string
	if w.Code != 201 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 3 || got["environment_id"] != environment || got["key_id"] != keyID || got["executor_token"] != "synthetic-connect-only" {
		t.Fatal("credential response", w.Code, w.Body)
	}
	// The Project's principal, not a caller, owns the credential; the write is audited.
	if f.principal.TenantID != key.TenantID || f.principal.SubjectID != key.SubjectID || f.environment != environment || f.rotate || !f.audited {
		t.Fatal("credential not bound to the Project")
	}
	if w := projectKeyHTTP(h, "POST", path, "admin", `{"key_id":"`+keyID+`","rotate":true}`); w.Code != 201 || !f.rotate {
		t.Fatal("explicit rotation", w.Code)
	}
	// key_id must be a canonical lowercase, non-nil UUID.
	for _, body := range []string{`{}`, `{"key_id":"invalid"}`, `{"key_id":null}`, `{"key_id":"` + strings.ToUpper(keyID) + `"}`,
		`{"key_id":"00000000-0000-0000-0000-000000000000"}`, `{"key_id":"{` + keyID + `}"}`, `{"key_id":"urn:uuid:` + keyID + `"}`, `{"key_id":"` + strings.ReplaceAll(keyID, "-", "") + `"}`, `{"key_id":"` + keyID + `","environment_id":"other"}`, `{"key_id":"` + keyID + `","executor_token":"import"}`, `{"key_id":"` + keyID + `","rotate":null}`} {
		before := f.calls
		if w := projectKeyHTTP(h, "POST", path, "admin", body); w.Code != 400 || before != f.calls {
			t.Fatal("invalid request accepted", w.Code, body)
		}
	}
	f.err = store.ErrExecutorCredentialExists
	if w := projectKeyHTTP(h, "POST", path, "admin", body); w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"executor_credential_exists"`) || strings.Contains(w.Body.String(), "synthetic-connect-only") {
		t.Fatal("uncertain retry", w.Code, w.Body)
	}
	f.err = store.ErrProjectArchived
	if w := projectKeyHTTP(h, "POST", path, "admin", body); w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"project_archived"`) {
		t.Fatal("archived Project", w.Code, w.Body)
	}
	f.err = store.ErrNotFound
	// Rotating a key_id that was never issued is not found.
	if w := projectKeyHTTP(h, "POST", path, "admin", `{"key_id":"`+uuid.NewString()+`","rotate":true}`); w.Code != 404 || !f.rotate {
		t.Fatal("unknown key rotation", w.Code)
	}
	if w := projectKeyHTTP(h, "DELETE", path+"/"+keyID, "admin", ""); w.Code != 404 {
		t.Fatal("foreign revocation", w.Code)
	}
	f.err = nil
	for range 2 {
		if w := projectKeyHTTP(h, "DELETE", path+"/"+keyID, "admin", ""); w.Code != 204 || w.Body.Len() != 0 || f.key != keyID {
			t.Fatal("revocation", w.Code)
		}
	}
}
