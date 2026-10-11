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
	t         testing.TB
	selection func(context.Context) (workspaces.Selection, error)
	configure func(context.Context, workspaces.SelectionInput) (workspaces.Selection, error)
}

func (f *fakeWorkspaceStorage) Selection(ctx context.Context) (workspaces.Selection, error) {
	if f.selection == nil {
		f.t.Fatal("unexpected WorkspaceStorage.Selection")
	}
	return f.selection(ctx)
}
func (f *fakeWorkspaceStorage) Configure(ctx context.Context, c workspaces.SelectionInput) (workspaces.Selection, error) {
	if f.configure == nil {
		f.t.Fatal("unexpected WorkspaceStorage.Configure")
	}
	return f.configure(ctx, c)
}

const workspaceConfigurationJSON = `{"id":"11111111-1111-4111-8111-111111111111","adapter":"directory","parameters":{"root":"/workspace-storage"},"tenant_capacity_mib":0}`

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
	var saved workspaces.Selection
	var credential json.RawMessage
	f.workspaceStorage.configure = func(ctx context.Context, c workspaces.SelectionInput) (workspaces.Selection, error) {
		source, ok := adminaudit.FromContext(ctx)
		if !ok || source.CredentialID == "" || source.ProjectID != "" {
			t.Fatalf("missing deployment audit source: %+v", source)
		}
		saved, credential = c.Selection, c.Credential
		return saved, nil
	}
	f.workspaceStorage.selection = func(context.Context) (workspaces.Selection, error) { return saved, nil }
	h := newTestHandler(t, deps)
	body := strings.Replace(workspaceConfigurationJSON, `"tenant_capacity_mib":0`, `"tenant_capacity_mib":8192,"credential":{"ssh_private_key":"synthetic-private-key"}`, 1)
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		result := workspaceStorageRequest(h, method, "admin", body)
		if result.Code != http.StatusOK || strings.Contains(result.Body.String(), "synthetic-private-key") || strings.Contains(result.Body.String(), "credential") {
			t.Fatalf("%s: %d %s", method, result.Code, result.Body.String())
		}
		var got workspaces.Selection
		if err := json.Unmarshal(result.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != saved.ID || got.Adapter != saved.Adapter || string(got.Parameters) != string(saved.Parameters) || got.TenantCapacityMiB != 8192 {
			t.Fatalf("configuration round trip: %+v", got)
		}
	}
	if string(credential) != `{"ssh_private_key":"synthetic-private-key"}` {
		t.Fatalf("write-only credential not forwarded: %s", credential)
	}
	if result := workspaceStorageRequest(h, http.MethodPut, "admin", workspaceConfigurationJSON); result.Code != http.StatusOK || credential != nil {
		t.Fatalf("omitted credential must be absent, not empty: %d %q", result.Code, credential)
	}
}

func TestWorkspaceStorageRejectsInvalidInput(t *testing.T) {
	deps, _ := testDependencies(t)
	h := newTestHandler(t, deps)
	for _, body := range []string{"null", "{}", `{"id":"not-a-uuid","adapter":"directory","parameters":{},"tenant_capacity_mib":0}`, strings.Replace(workspaceConfigurationJSON, `,"tenant_capacity_mib":0`, ``, 1), strings.Replace(workspaceConfigurationJSON, `"tenant_capacity_mib":0`, `"tenant_capacity_mib":-1`, 1), strings.Replace(workspaceConfigurationJSON, `"parameters":{`, `"host_path":"/arbitrary","parameters":{`, 1), strings.Replace(workspaceConfigurationJSON, `"parameters":{"root":"/workspace-storage"}`, `"parameters":null`, 1), strings.Replace(workspaceConfigurationJSON, `"id":`, `"ID":`, 1)} {
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
		{workspacefs.ErrUnavailable, 503}, {workspacefs.ErrUnconfirmed, 503}, {workspaces.ErrCapacityExhausted, 503},
		{fmt.Errorf("unknown /secret/native/path"), 500},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			deps, f := testDependencies(t)
			err := fmt.Errorf("/secret/native/path: %w", tc.err)
			f.workspaceStorage.selection = func(context.Context) (workspaces.Selection, error) { return workspaces.Selection{}, err }
			f.workspaceStorage.configure = func(context.Context, workspaces.SelectionInput) (workspaces.Selection, error) {
				return workspaces.Selection{}, err
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
