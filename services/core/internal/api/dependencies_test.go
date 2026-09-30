package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// testExecutorURL is the daemon URL self-hosted Sessions report in tests.
const testExecutorURL = "wss://core.example/api/v1/agent-daemon/ws"

// testFakes holds one strict fake per Dependencies area.
type testFakes struct {
	projects               *fakeProjects
	vaults                 *fakeVaults
	modelProviders         *fakeModelProviders
	files                  *fakeFiles
	skills                 *fakeSkills
	environmentTemplates   *fakeEnvironmentTemplates
	agents                 *fakeAgents
	sessions               *fakeSessions
	sessionEvents          *fakeSessionEvents
	sessionHistory         *fakeSessionHistory
	subagents              *fakeSubagents
	artifacts              *fakeArtifacts
	sessionAdmin           *fakeSessionAdmin
	environments           *fakeEnvironments
	executorConnections    *fakeExecutorConnections
	admin                  *fakeAdmin
	writeAudit             *fakeWriteAudit
	metrics                *fakeMetrics
	runtimeObservations    *fakeRuntimeObservations
	runtimeHistory         *fakeRuntimeHistory
	installationBindings   *fakeInstallationBindings
	admission              *fakeAdmission
	sessionArchive         *fakeSessionArchive
	workspaces             *fakeEnvironmentWorkspaces
	deployment             *fakeDeployment
	deploymentChanges      *fakeDeploymentChanges
	configurationDiscovery *fakeConfigurationDiscovery
}

// testDependencies returns Dependencies in which every area is a strict fake.
// A test sets the funcs it expects on the returned fakes; any other call fails
// it. Engine is "codex", CoreKeys accepts "Bearer admin", and Execution and
// Sandboxes are disabled until the test sets fakes.execution() or
// fakes.sandboxes().
func testDependencies(t testing.TB) (Dependencies, *testFakes) {
	t.Helper()
	f := &testFakes{
		projects: &fakeProjects{t: t}, vaults: &fakeVaults{t: t}, modelProviders: &fakeModelProviders{t: t},
		files: &fakeFiles{t: t}, skills: &fakeSkills{t: t}, environmentTemplates: &fakeEnvironmentTemplates{t: t},
		agents: &fakeAgents{t: t}, sessions: &fakeSessions{t: t}, sessionEvents: &fakeSessionEvents{t: t},
		sessionHistory: &fakeSessionHistory{t: t}, subagents: &fakeSubagents{t: t}, artifacts: &fakeArtifacts{t: t},
		sessionAdmin: &fakeSessionAdmin{t: t}, environments: &fakeEnvironments{t: t}, executorConnections: &fakeExecutorConnections{t: t},
		admin: &fakeAdmin{t: t}, writeAudit: &fakeWriteAudit{t: t}, metrics: &fakeMetrics{t: t},
		runtimeObservations: &fakeRuntimeObservations{t: t}, runtimeHistory: &fakeRuntimeHistory{t: t}, installationBindings: &fakeInstallationBindings{t: t},
		admission: &fakeAdmission{t: t}, sessionArchive: &fakeSessionArchive{t: t}, workspaces: &fakeEnvironmentWorkspaces{t: t},
		deployment: &fakeDeployment{t: t}, deploymentChanges: &fakeDeploymentChanges{t: t}, configurationDiscovery: &fakeConfigurationDiscovery{t: t},
	}
	return Dependencies{
		Engine: "codex", CoreKeys: coreKeys(t, "admin"), InstallationBindings: f.installationBindings,
		Projects: f.projects, Vaults: f.vaults, ModelProviders: f.modelProviders, Files: f.files, Skills: f.skills,
		EnvironmentTemplates: f.environmentTemplates, Agents: f.agents, Sessions: f.sessions, SessionEvents: f.sessionEvents,
		SessionHistory: f.sessionHistory, Subagents: f.subagents, Artifacts: f.artifacts, SessionAdmin: f.sessionAdmin,
		Environments: f.environments, ExecutorConnections: f.executorConnections, Admin: f.admin, WriteAudit: f.writeAudit,
		Metrics: f.metrics, RuntimeObservations: f.runtimeObservations, RuntimeHistory: f.runtimeHistory,
	}, f
}

// execution is an Execution group backed by f's strict fakes, reporting
// testExecutorURL and without a native installer.
func (f *testFakes) execution() *Execution {
	return &Execution{ExecutorURL: testExecutorURL, Admission: f.admission, SessionArchive: f.sessionArchive, Workspaces: f.workspaces}
}

// sandboxes is a Sandboxes group backed by f's strict fakes. It requires
// Execution.
func (f *testFakes) sandboxes() *Sandboxes {
	return &Sandboxes{Deployment: f.deployment, DeploymentChanges: f.deploymentChanges, ConfigurationDiscovery: f.configurationDiscovery}
}

// coreKeys accepts each token as a Core key.
func coreKeys(t testing.TB, tokens ...string) *DeploymentAuthenticator {
	t.Helper()
	digests := make([]string, len(tokens))
	for i, token := range tokens {
		digests[i] = runtimedevice.HashCredential(token)
	}
	keys, err := NewDeploymentAuthenticator(digests)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// newTestHandler builds the handler from deps and fails the test when
// NewHandler rejects them.
func newTestHandler(t testing.TB, deps Dependencies) http.Handler {
	t.Helper()
	h, err := NewHandler(deps)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// noDeploymentModelProvider is a deployment without a default model provider.
func noDeploymentModelProvider(context.Context, string) (*store.DeploymentModelProviderSnapshot, error) {
	return nil, nil
}

func TestNewHandlerAcceptsCompleteDependencies(t *testing.T) {
	deps, f := testDependencies(t)
	if _, err := NewHandler(deps); err != nil {
		t.Fatal(err)
	}
	deps.Execution = f.execution()
	deps.Execution.NativeInstaller = &NativeInstaller{Version: "build"}
	deps.Sandboxes = f.sandboxes()
	if _, err := NewHandler(deps); err != nil {
		t.Fatal(err)
	}
}

func TestNewHandlerRejectsIncompleteDependencies(t *testing.T) {
	for _, test := range []struct {
		missing string
		change  func(*Dependencies, *testFakes)
	}{
		{"default Harness", func(d *Dependencies, _ *testFakes) { d.Engine = "" }},
		{"CoreKeys", func(d *Dependencies, _ *testFakes) { d.CoreKeys = nil }},
		{"InstallationBindings", func(d *Dependencies, _ *testFakes) { d.InstallationBindings = nil }},
		{"Projects", func(d *Dependencies, _ *testFakes) { d.Projects = nil }},
		{"Sessions", func(d *Dependencies, _ *testFakes) { d.Sessions = nil }},
		{"RuntimeHistory", func(d *Dependencies, _ *testFakes) { d.RuntimeHistory = nil }},
		{"Execution.ExecutorURL", func(d *Dependencies, f *testFakes) {
			d.Execution = f.execution()
			d.Execution.ExecutorURL = ""
		}},
		{"Execution.Admission", func(d *Dependencies, f *testFakes) {
			d.Execution = f.execution()
			d.Execution.Admission = nil
		}},
		{"Execution.NativeInstaller.Version", func(d *Dependencies, f *testFakes) {
			d.Execution = f.execution()
			d.Execution.NativeInstaller = &NativeInstaller{}
		}},
		{"Sandboxes requires Execution", func(d *Dependencies, f *testFakes) { d.Sandboxes = f.sandboxes() }},
		{"Sandboxes.ConfigurationDiscovery", func(d *Dependencies, f *testFakes) {
			d.Execution, d.Sandboxes = f.execution(), f.sandboxes()
			d.Sandboxes.ConfigurationDiscovery = nil
		}},
	} {
		t.Run(test.missing, func(t *testing.T) {
			deps, f := testDependencies(t)
			test.change(&deps, f)
			if h, err := NewHandler(deps); err == nil || h != nil || !strings.Contains(err.Error(), test.missing) {
				t.Fatalf("NewHandler = %v, %v", h, err)
			}
		})
	}
}
