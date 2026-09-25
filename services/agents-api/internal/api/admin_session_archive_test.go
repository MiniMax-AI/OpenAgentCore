package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type archiveManagementFixture struct {
	AdminManagementStore
	tenant, session       string
	generation            uint64
	calls                 int
	audited, impersonated bool
	err                   error
}

func (s *archiveManagementFixture) ArchiveManagedSession(ctx context.Context, tenant, session string, generation uint64) (store.ManagedSessionArchive, error) {
	s.calls++
	s.tenant, s.session, s.generation = tenant, session, generation
	source, ok := adminaudit.FromContext(ctx)
	s.audited = ok && source.ProjectID == managementProjectID && source.CredentialID != "" && source.RequestID != ""
	s.impersonated = ctx.Value(principalContextKey{}) != nil
	return store.ManagedSessionArchive{SessionID: session, EnvironmentID: "environment", State: "cleanup_pending"}, s.err
}

func (s *archiveManagementFixture) GetManagedSessionArchive(_ context.Context, tenant, session string) (store.ManagedSessionArchive, error) {
	s.calls++
	s.tenant, s.session = tenant, session
	return store.ManagedSessionArchive{SessionID: session, EnvironmentID: "environment", State: "released"}, s.err
}

func TestAdminSessionArchiveAuthorityAndValidation(t *testing.T) {
	key := callerBinding()
	auth, _ := NewAuthenticator([]APIKey{key})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("admin")})
	fixture := &archiveManagementFixture{}
	h, err := NewHandler(&recordingStore{}, auth, "codex", WithProjectAPIKeys(managementProjectStore(key), admin), WithAdminManagement(fixture), WithSessionArchive(fixture.ArchiveManagedSession))
	if err != nil {
		t.Fatal(err)
	}
	path := "/core/v1/projects/" + managementProjectID + "/sessions/11111111-1111-4111-8111-111111111111/archive"
	for _, body := range []string{`{}`, `{"expected_generation":null}`, `{"expected_generation":0}`, `{"expected_generation":-1}`, `{"expected_generation":1.5}`, `{"expected_generation":"1"}`, `{"Expected_Generation":1}`} {
		if w := projectKeyHTTP(h, http.MethodPost, path, "admin", body); w.Code != 400 {
			t.Fatalf("invalid archive body accepted: %s: %d %s", body, w.Code, w.Body)
		}
	}
	for _, token := range []string{"", "caller", "issued-project-key"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if w := projectKeyHTTP(h, method, path, token, `{"expected_generation":1}`); w.Code != 401 {
				t.Fatalf("archive authorized %q: %d", token, w.Code)
			}
		}
	}
	if fixture.calls != 0 {
		t.Fatal("invalid request reached store")
	}
	w := projectKeyHTTP(h, http.MethodPost, path, "admin", `{"expected_generation":2}`)
	var result store.ManagedSessionArchive
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.State != "cleanup_pending" {
		t.Fatalf("archive: %d %s", w.Code, w.Body)
	}
	if fixture.tenant != key.TenantID || fixture.generation != 2 || !fixture.audited || fixture.impersonated {
		t.Fatalf("incorrect administrative target: %+v", fixture)
	}
	w = projectKeyHTTP(h, http.MethodGet, path, "admin", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.State != "released" {
		t.Fatalf("archive status: %d %s", w.Code, w.Body)
	}
	for _, failure := range []struct {
		err    error
		status int
	}{{store.ErrSandboxDeploymentConflict, 409}, {store.ErrNotFound, 404}, {store.ErrInvalidInput, 400}} {
		fixture.err = failure.err
		if w := projectKeyHTTP(h, http.MethodPost, path, "admin", `{"expected_generation":2}`); w.Code != failure.status {
			t.Fatalf("archive error: %d %s", w.Code, w.Body)
		}
	}
}
