package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

type fakeWorkspaceStorage struct {
	t             testing.TB
	configuration func(context.Context) (workspacefs.Configuration, error)
	configure     func(context.Context, workspacefs.Configuration) (workspacefs.Configuration, error)
}

func (f *fakeWorkspaceStorage) Configuration(ctx context.Context) (workspacefs.Configuration, error) {
	if f.configuration == nil {
		f.t.Fatal("unexpected WorkspaceStorage.Configuration")
	}
	return f.configuration(ctx)
}
func (f *fakeWorkspaceStorage) Configure(ctx context.Context, c workspacefs.Configuration) (workspacefs.Configuration, error) {
	if f.configure == nil {
		f.t.Fatal("unexpected WorkspaceStorage.Configure")
	}
	return f.configure(ctx, c)
}

const workspaceConfigurationJSON = `{"id":"11111111-1111-4111-8111-111111111111","adapter":"directory","parameters":{"root":"/workspace-storage"}}`

func workspaceStorageRequest(h http.Handler, method, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/core/v1/workspace-storage", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	result := httptest.NewRecorder()
	h.ServeHTTP(result, request)
	return result
}

func TestWorkspaceStorageAuthentication(t *testing.T) {
	deps, _ := testDependencies(t)
	h := newTestHandler(t, deps)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		for _, token := range []string{"", "project-key", "node-credential"} {
			result := workspaceStorageRequest(h, method, token, workspaceConfigurationJSON)
			if result.Code != http.StatusUnauthorized {
				t.Fatalf("%s %q: %d %s", method, token, result.Code, result.Body.String())
			}
		}
	}
}

func TestWorkspaceStorageRoundTrip(t *testing.T) {
	deps, f := testDependencies(t)
	var saved workspacefs.Configuration
	f.workspaceStorage.configure = func(ctx context.Context, c workspacefs.Configuration) (workspacefs.Configuration, error) {
		source, ok := adminaudit.FromContext(ctx)
		if !ok || source.CredentialID == "" || source.ProjectID != "" {
			t.Fatalf("missing deployment audit source: %+v", source)
		}
		saved = c
		return saved, nil
	}
	f.workspaceStorage.configuration = func(context.Context) (workspacefs.Configuration, error) { return saved, nil }
	h := newTestHandler(t, deps)
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		result := workspaceStorageRequest(h, method, "admin", workspaceConfigurationJSON)
		if result.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", method, result.Code, result.Body.String())
		}
		var got workspacefs.Configuration
		if err := json.Unmarshal(result.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != saved.ID || got.Adapter != saved.Adapter || string(got.Parameters) != string(saved.Parameters) {
			t.Fatalf("configuration round trip: %+v", got)
		}
	}
}

func TestWorkspaceStorageRejectsInvalidInput(t *testing.T) {
	deps, _ := testDependencies(t)
	h := newTestHandler(t, deps)
	for _, body := range []string{"null", "{}", `{"id":"not-a-uuid","adapter":"directory","parameters":{}}`, strings.Replace(workspaceConfigurationJSON, `"parameters":{`, `"host_path":"/arbitrary","parameters":{`, 1), strings.Replace(workspaceConfigurationJSON, `"parameters":{"root":"/workspace-storage"}`, `"parameters":null`, 1), strings.Replace(workspaceConfigurationJSON, `"id":`, `"ID":`, 1)} {
		result := workspaceStorageRequest(h, http.MethodPut, "admin", body)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, result.Code, result.Body.String())
		}
	}
}

func TestWorkspaceStorageSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{workspaces.ErrNotConfigured, 404}, {workspacefs.ErrNotFound, 404},
		{workspacefs.ErrInvalid, 400}, {workspacefs.ErrUnsupported, 400},
		{workspacefs.ErrOwnership, 409}, {workspaces.ErrConflict, 409},
		{workspacefs.ErrUnavailable, 503}, {workspacefs.ErrUnconfirmed, 503},
		{fmt.Errorf("unknown /secret/native/path"), 500},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			deps, f := testDependencies(t)
			err := fmt.Errorf("/secret/native/path: %w", tc.err)
			f.workspaceStorage.configuration = func(context.Context) (workspacefs.Configuration, error) { return workspacefs.Configuration{}, err }
			f.workspaceStorage.configure = func(context.Context, workspacefs.Configuration) (workspacefs.Configuration, error) {
				return workspacefs.Configuration{}, err
			}
			h := newTestHandler(t, deps)
			for _, method := range []string{http.MethodGet, http.MethodPut} {
				result := workspaceStorageRequest(h, method, "admin", workspaceConfigurationJSON)
				if result.Code != tc.status || strings.Contains(result.Body.String(), "/secret") {
					t.Fatalf("%s: %d %s", method, result.Code, result.Body.String())
				}
			}
		})
	}
}

func TestWorkspaceUnsupportedPublicAdmissionError(t *testing.T) {
	result := httptest.NewRecorder()
	writeOperationError(result, httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", nil), fmt.Errorf("/secret/native/path: %w", workspacefs.ErrUnsupported))
	if result.Code != http.StatusBadRequest || strings.Contains(result.Body.String(), "/secret") {
		t.Fatalf("%d %s", result.Code, result.Body.String())
	}
}
