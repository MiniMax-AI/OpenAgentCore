package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type executorManagementFixture struct {
	principal           identity.Principal
	environment, key    string
	rotate, audited     bool
	calls               int
	err                 error
	connection          store.ExecutorConnectionState
	resolvedEnvironment string
}

func (f *executorManagementFixture) record(ctx context.Context, principal identity.Principal, environment, key string) {
	f.principal, f.environment, f.key = principal, environment, key
	_, f.audited = adminaudit.FromContext(ctx)
	f.calls++
}
func (f *executorManagementFixture) ProjectExecutorCredentialState(ctx context.Context, principal identity.Principal, environment string) (store.ExecutorCredentialState, error) {
	f.record(ctx, principal, environment, "")
	resolved := f.resolvedEnvironment
	if resolved == "" {
		resolved = environment
	}
	return store.ExecutorCredentialState{EnvironmentID: resolved, Credentials: []store.ExecutorCredential{{KeyID: "listed", CreatedAt: time.Unix(1, 0).UTC()}}, Connection: f.connection}, f.err
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

// executorManagementHandler serves f's executor credentials to the Core key
// "admin" for managementProjectID, observing live connections with connected.
func executorManagementHandler(t *testing.T, key APIKey, f *executorManagementFixture, connected func(context.Context, string, string) (bool, error)) http.Handler {
	t.Helper()
	deps, fakes := managementFakes(t, key)
	fakes.environments.projectExecutorCredentialState = f.ProjectExecutorCredentialState
	fakes.environments.issueProjectExecutorCredential = f.IssueProjectExecutorCredential
	fakes.environments.revokeProjectExecutorCredential = f.RevokeProjectExecutorCredential
	fakes.executorConnections.executorConnected = connected
	return newTestHandler(t, deps)
}

func TestProjectExecutorCredentialsHTTP(t *testing.T) {
	f := &executorManagementFixture{}
	key := callerBinding()
	h := executorManagementHandler(t, key, f, nil)
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
	if w.Code != 200 || w.Body.String() != `{"data":[{"key_id":"listed","created_at":"1970-01-01T00:00:01Z","revoked_at":null}],"connection":{"status":"never_enrolled","bound_key_id":null,"enrolled_at":null,"last_seen_at":null}}`+"\n" {
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

func TestExecutorConnectionListObservation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		connected bool
		err       error
		want      string
		status    int
	}{
		{"live", true, nil, "connected", 200}, {"closed", false, nil, "disconnected", 200},
		{"rotated", false, store.ErrDeviceBindingConflict, "disconnected", 200},
		{"revoked", false, store.ErrNotFound, "disconnected", 200}, {"database failure", false, errors.New("private-database"), "", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := callerBinding()
			at := time.Unix(1, 0).UTC()
			bound := "bound-key"
			f := &executorManagementFixture{connection: store.ExecutorConnectionState{DeviceID: "device", BoundKeyID: &bound, EnrolledAt: &at, CredentialHash: "private-digest", EnvironmentStatus: "connected"}}
			h := executorManagementHandler(t, key, f, func(_ context.Context, environment, digest string) (bool, error) {
				if environment != "environment" || digest != "private-digest" {
					t.Fatal("wrong binding")
				}
				return tc.connected, tc.err
			})
			w := projectKeyHTTP(h, "GET", "/core/v1/projects/"+managementProjectID+"/environments/environment/executor-credentials", "admin", "")
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), "private-") {
				t.Fatal("internal observation leaked")
			}
			if tc.status == 200 {
				var got ExecutorCredentialList
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Connection.Status != tc.want || got.Connection.BoundKeyID == nil || *got.Connection.BoundKeyID != bound {
					t.Fatal("projection", err, w.Body)
				}
			}
		})
	}
}

func TestExecutorConnectionListUsesResolvedEnvironment(t *testing.T) {
	const canonical = "a21e4155-d8bb-4e99-afce-273a907efbc8"
	key := callerBinding()
	f := &executorManagementFixture{
		resolvedEnvironment: canonical,
		connection:          store.ExecutorConnectionState{DeviceID: "device", CredentialHash: "private-digest", EnvironmentStatus: "connected"},
	}
	observations := 0
	h := executorManagementHandler(t, key, f, func(_ context.Context, environment, digest string) (bool, error) {
		observations++
		if environment != canonical || digest != "private-digest" {
			return false, store.ErrDeviceBindingConflict
		}
		return true, nil
	})
	for _, spelling := range []string{canonical, strings.ToUpper(canonical), strings.ReplaceAll(canonical, "-", "")} {
		w := projectKeyHTTP(h, "GET", "/core/v1/projects/"+managementProjectID+"/environments/"+spelling+"/executor-credentials", "admin", "")
		var got ExecutorCredentialList
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Connection.Status != "connected" {
			t.Fatalf("equivalent target %q: %d %s", spelling, w.Code, w.Body.String())
		}
		if f.environment != spelling {
			t.Fatal("target spelling did not reach store resolution")
		}
	}
	if observations != 3 {
		t.Fatal("missing live authority observations", observations)
	}
}
