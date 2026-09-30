package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeenrollment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// Each credential works only in its own namespace: a Project API key in /v1,
// the Core key in /core/v1, and node and executor credentials only on their
// own /api/v1 machine connection routes.
func TestCredentialNamespaceMatrix(t *testing.T) {
	s, _ := store.NewManagedTestStore(t)
	s.SetPublicURL("https://core.example")
	ctx := t.Context()
	coreKey := uuid.NewString()
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential(coreKey)})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewDatabaseAuthenticator(s)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex", api.WithSandboxManager(s, admin), api.WithProjectAPIKeys(s, admin))
	if err != nil {
		t.Fatal(err)
	}
	// The server composition: daemon transport beside the API handler.
	mux := http.NewServeMux()
	mux.Handle("/api/v1/agent-daemon/enroll", runtimeenrollment.EnrollmentHandler(s))
	mux.Handle("/api/v1/agent-daemon/connection", runtimeenrollment.ConnectionHandler(s, runtimegateway.NewRegistry()))
	mux.Handle("/", handler)
	server := api.CanonicalPaths(mux)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	created := func(method, path, token, body string, value any) {
		t.Helper()
		if w := call(method, path, token, body); w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), value) != nil {
			t.Fatalf("%s %s = %d %s", method, path, w.Code, w.Body)
		}
	}

	// A Project and its API key, issued with the Core key.
	var project store.Project
	created("POST", "/core/v1/projects", coreKey, `{"name":"Matrix"}`, &project)
	var projectKey store.IssuedProjectAPIKey
	created("POST", "/core/v1/projects/"+project.ID+"/keys", coreKey, `{"name":"application"}`, &projectKey)

	// An executor credential for a self_hosted Session of that Project.
	binding, err := s.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, binding.Principal.TenantID, store.CreateSessionInput{Creator: binding.Principal.Subject(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, binding.Principal.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var executor store.IssuedExecutorCredential
	created("POST", "/core/v1/projects/"+project.ID+"/environments/"+environment.ID+"/executor-credentials", coreKey, `{"key_id":"`+uuid.NewString()+`"}`, &executor)

	// A node credential: a Docker deployment, an enrollment token issued with the Core key, and an enrolled node.
	installation := uuid.NewString()
	provider := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	runtimes := execution.NewDeferredRuntimeProvider(installation, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, BackendFingerprint: setup.BackendFingerprint, CoreURL: "https://core.example/api/v1", Provider: provider}, nil
	}, func(_ context.Context, setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
		return execution.PreparedRuntimeDeployment{Config: &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}}, nil
	})
	worker, err := execution.StartWorker(ctx, &execution.Dispatcher{Store: s, Registry: runtimegateway.NewRegistry(), ManagedRuntimes: runtimes})
	if err != nil {
		t.Fatal(err)
	}
	var stop sync.Once
	t.Cleanup(func() {
		stop.Do(func() {
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			_ = worker.Run(cancelled)
		})
	})
	specification := store.SandboxDeploymentTestSpec("docker")
	if _, err := worker.InitializeSandboxDeployment(ctx, store.SandboxDeploymentSetupRequest{DeploymentSpec: specification, Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	var enrollment api.SandboxEnrollmentToken
	created("POST", "/core/v1/sandbox/enrollment-tokens", coreKey, `{}`, &enrollment)
	nodeID, nodeCredential := uuid.NewString(), strings.Repeat("n", 64)
	enroll, _ := json.Marshal(store.RuntimeNodeEnrollment{NodeID: nodeID, Credential: nodeCredential, Name: "Matrix node", Provider: "docker", BackendFingerprint: strings.Repeat("b", 64),
		DeploymentGeneration: 1, SpecificationDigest: specification.Digest("docker"), CoreURL: "https://core.example"})
	var node store.RuntimeNodeIdentity
	created("POST", "/api/v1/sandbox-node/enroll", enrollment.Token, string(enroll), &node)

	// A second, unconsumed enrollment token; its only uses are enroll and configuration.
	var unused api.SandboxEnrollmentToken
	created("POST", "/core/v1/sandbox/enrollment-tokens", coreKey, `{}`, &unused)

	routes := []struct{ name, method, path, body string }{
		{"/v1", "GET", "/v1/agents", ""},
		{"/core/v1", "GET", "/core/v1/projects", ""},
		{"node identity", "GET", "/api/v1/sandbox-node/identity?node_id=" + nodeID, ""},
		{"node configuration", "GET", "/api/v1/sandbox-node/configuration", ""},
		{"daemon connection", "GET", "/api/v1/agent-daemon/connection?environment_id=" + environment.ID, ""},
		{"daemon enroll", "POST", "/api/v1/agent-daemon/enroll", `{"environment_id":"` + environment.ID + `"}`},
	}
	for _, credential := range []struct {
		name, token string
		own         []string
	}{
		{"Project API key", projectKey.Key, []string{"/v1"}},
		{"Core key", coreKey, []string{"/core/v1"}},
		{"node enrollment token", unused.Token, []string{"node configuration"}},
		{"node credential", nodeCredential, []string{"node identity"}},
		{"executor credential", executor.Token, []string{"daemon connection", "daemon enroll"}},
	} {
		for _, route := range routes {
			want := http.StatusUnauthorized
			if slices.Contains(credential.own, route.name) {
				want = http.StatusOK
			}
			if w := call(route.method, route.path, credential.token, route.body); w.Code != want {
				t.Errorf("%s on %s = %d, want %d: %s", credential.name, route.name, w.Code, want, w.Body)
			}
		}
	}
}
