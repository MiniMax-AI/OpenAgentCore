package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

type installationFixture struct {
	environmentCreationFixture
	authorizedEnvironment string
}

func (f *installationFixture) AuthorizeEnvironmentInstallation(_ context.Context, p identity.Principal, environment, version string) (string, int64, error) {
	if p.TenantID != f.session.TenantID || environment != f.session.Environment.ID || version != "build" {
		return "", 0, sessions.ErrNotFound
	}
	f.authorizedEnvironment = environment
	return "short-lived-install-grant", 2000000000, nil
}
func (f *installationFixture) ValidateEnvironmentInstallation(context.Context, string, string) (sessions.InstallationAuthorization, error) {
	return sessions.InstallationAuthorization{}, sessions.ErrInstallationAuthorization
}

func TestSelfHostedCreationReturnsInstallationWithoutWebCredential(t *testing.T) {
	f := &installationFixture{}
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = projectKeys(t, APIKey{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential("project-key"), TenantID: uuid.NewString()}).ResolveAPIKey
	fakes.sessionCreation.findSessionCreation, fakes.sessionCreation.createSession = f.FindSessionCreation, f.CreateSession
	fakes.modelProviders.resolve = fixtureDeploymentProvider
	fakes.environments.authorizeEnvironmentInstallation, fakes.environments.validateEnvironmentInstallation = f.AuthorizeEnvironmentInstallation, f.ValidateEnvironmentInstallation
	deps.Execution = fakes.execution()
	deps.Execution.NativeInstaller = &NativeInstaller{Version: "build", Catalog: &nativeinstaller.Catalog{Version: "build"}}
	handler := newTestHandler(t, deps)
	body := `{"agent":{"model":"model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"},"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://model.example/v1","api_key":"fixture-model"}}}`
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer project-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	var response v1.Session
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.XAgentsCore == nil || response.XAgentsCore.Installation == nil {
		t.Fatal("installation omitted")
	}
	if f.authorizedEnvironment != response.Environment.ID || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("wrong authorization scope or caching")
	}
	for _, shell := range []string{"posix", "powershell"} {
		command := response.XAgentsCore.Installation.Commands[shell]
		if !strings.Contains(command, "short-lived-install-grant") || strings.Contains(command, "project-key") || strings.Contains(command, "fixture-model") {
			t.Fatal("incorrect command authority")
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent-daemon/installation", nil)
	request.Header.Set("Authorization", "Bearer "+response.Environment.ID)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("Environment ID authenticated installation", w.Code)
	}
}
