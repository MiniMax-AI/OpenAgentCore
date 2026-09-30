package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// testExecutorURL is the daemon URL self-hosted Sessions report unless a test
// sets another with executorURL.
const testExecutorURL = "wss://core.example/api/v1/agent-daemon/ws"

// publicHandler serves s through api.NewHandler. s backs every area the Store
// implements, and db is the database and credential key that built s; the
// audit reads come from db. keys authenticate as Project keys and "admin" as
// the Core key. Metrics, Runtime observation and history, and executor
// connections are strict stand-ins. Execution and Sandboxes stay disabled
// unless configure sets them.
func publicHandler(t testing.TB, s *store.Store, db fixtureDB, keys fixtureKeyResolver, engine string, configure ...func(*api.Dependencies)) (http.Handler, error) {
	t.Helper()
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("admin")})
	if err != nil {
		return nil, err
	}
	strict := strictStandIn{t}
	audit := auditpg.New(pgunit.NewPool(db.pool))
	deps := api.Dependencies{
		Engine: engine, CoreKeys: admin, InstallationBindings: s,
		Projects: fixtureProjects{Store: s, keys: keys}, Vaults: s, ModelProviders: s, Files: s, Skills: s,
		EnvironmentTemplates: s, Agents: s, Sessions: s, SessionEvents: s, SessionHistory: s, Subagents: s,
		Artifacts: s, SessionAdmin: s, Environments: s, Admin: s, AdminAudit: audit, WriteAudit: audit,
		ExecutorConnections: strict, Metrics: strict, RuntimeObservations: strict, RuntimeHistory: strict,
	}
	for _, c := range configure {
		c(&deps)
	}
	return api.NewHandler(deps)
}

// fixtureProjects serves Projects from the Store and resolves Project keys from
// the test's fixture keys.
type fixtureProjects struct {
	*store.Store
	keys fixtureKeyResolver
}

func (p fixtureProjects) ResolveProjectAPIKey(ctx context.Context, digest string) (store.ProjectAPIKeyBinding, error) {
	return p.keys.ResolveProjectAPIKey(ctx, digest)
}

// storeKeys resolves Project keys from the Store, as production does.
func storeKeys(s *store.Store) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.Projects = s }
}

// withCoreKeys replaces the Core key.
func withCoreKeys(keys *api.DeploymentAuthenticator) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.CoreKeys = keys }
}

// withHarnesses enables Harnesses besides the default for explicit selection.
func withHarnesses(kinds []string) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.Harnesses = kinds }
}

// withPolicy replaces the built-in Harness qualification.
func withPolicy(policy execution.Policy) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.Policy = policy }
}

// storeExecution admits Sessions and inputs through the Store without a
// Worker, so nothing runs them.
func storeExecution(t testing.TB, s *store.Store) func(*api.Dependencies) {
	return func(d *api.Dependencies) {
		d.Execution = &api.Execution{ExecutorURL: testExecutorURL, Admission: s, SessionArchive: strictStandIn{t}, Workspaces: strictStandIn{t}}
	}
}

// workerExecution runs Sessions through worker.
func workerExecution(worker *execution.Worker) func(*api.Dependencies) {
	return func(d *api.Dependencies) {
		d.Execution = &api.Execution{ExecutorURL: testExecutorURL, Admission: worker, SessionArchive: worker, Workspaces: worker}
	}
}

// executorURL replaces the daemon URL self-hosted Sessions report. It follows
// the option that enables Execution.
func executorURL(url string) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.Execution.ExecutorURL = url }
}

// managedSandboxes enables the Store's managed sandbox deployment: its
// administration routes and openai_hosted Environments. It follows the option
// that enables Execution.
func managedSandboxes(t testing.TB, s *store.Store) func(*api.Dependencies) {
	return func(d *api.Dependencies) {
		d.Sandboxes = &api.Sandboxes{Deployment: s, DeploymentChanges: strictStandIn{t}, ConfigurationDiscovery: strictStandIn{t}}
	}
}

// modelProviderDefaults resolves deployment model provider defaults with
// resolve instead of the Store's deployment configuration.
func modelProviderDefaults(s *store.Store, resolve func(context.Context, string) (*store.DeploymentModelProviderSnapshot, error)) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.ModelProviders = resolvedModelProviders{Store: s, resolve: resolve} }
}

type resolvedModelProviders struct {
	*store.Store
	resolve func(context.Context, string) (*store.DeploymentModelProviderSnapshot, error)
}

func (p resolvedModelProviders) DeploymentModelProvider(ctx context.Context, harness string) (*store.DeploymentModelProviderSnapshot, error) {
	return p.resolve(ctx, harness)
}

// acceptUnavailable lets the handler count execution_unavailable responses for
// tests that expect them.
func acceptUnavailable(t testing.TB) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.Metrics = unavailableMetrics{strictStandIn{t}} }
}

type unavailableMetrics struct{ strictStandIn }

func (unavailableMetrics) RecordUnavailable() {}

// strictStandIn fails the test on any call. It stands in for the areas a Store
// test does not exercise.
type strictStandIn struct{ t testing.TB }

func (s strictStandIn) unexpected(method string) {
	s.t.Helper()
	s.t.Fatalf("unexpected call to %s", method)
}

func (s strictStandIn) Read(context.Context, string) (coremetrics.View, error) {
	s.unexpected("Read")
	return coremetrics.View{}, nil
}

func (s strictStandIn) RecordUnavailable() { s.unexpected("RecordUnavailable") }

func (s strictStandIn) ObserveSession(context.Context, string, string) (runtimeobs.Observation, error) {
	s.unexpected("ObserveSession")
	return runtimeobs.Observation{}, nil
}

func (s strictStandIn) ObserveSessions(context.Context, []runtimeobs.SessionIdentity, runtimeobs.PageOptions) ([]runtimeobs.Observation, []error) {
	s.unexpected("ObserveSessions")
	return nil, nil
}

func (s strictStandIn) Capabilities() runtimehistory.Capabilities {
	s.unexpected("Capabilities")
	return runtimehistory.Capabilities{}
}

func (s strictStandIn) QuerySession(context.Context, string, string, runtimehistory.Range) (runtimehistory.Response, error) {
	s.unexpected("QuerySession")
	return runtimehistory.Response{}, nil
}

func (s strictStandIn) ExecutorConnected(context.Context, string, string) (bool, error) {
	s.unexpected("ExecutorConnected")
	return false, nil
}

func (s strictStandIn) ArchiveManagedSession(context.Context, string, string, uint64) (store.ManagedSessionArchive, error) {
	s.unexpected("ArchiveManagedSession")
	return store.ManagedSessionArchive{}, nil
}

func (s strictStandIn) ReadEnvironmentDirectory(context.Context, store.Environment, string) (proto.WorkspaceDirectoryResult, error) {
	s.unexpected("ReadEnvironmentDirectory")
	return proto.WorkspaceDirectoryResult{}, nil
}

func (s strictStandIn) WriteEnvironmentFile(context.Context, store.Environment, string, []byte) (int64, error) {
	s.unexpected("WriteEnvironmentFile")
	return 0, nil
}

func (s strictStandIn) DiscoverConfiguration(context.Context, string, sandbox.ConfigurationDiscoveryInput) (json.RawMessage, error) {
	s.unexpected("DiscoverConfiguration")
	return nil, nil
}

func (s strictStandIn) InitializeSandboxDeployment(context.Context, store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
	s.unexpected("InitializeSandboxDeployment")
	return store.RuntimeDeploymentView{}, nil
}

func (s strictStandIn) UpdateSandboxDeployment(context.Context, store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error) {
	s.unexpected("UpdateSandboxDeployment")
	return store.RuntimeDeploymentView{}, nil
}

func (s strictStandIn) StartSandboxReset(context.Context, store.SandboxResetRequest) (store.RuntimeDeploymentView, error) {
	s.unexpected("StartSandboxReset")
	return store.RuntimeDeploymentView{}, nil
}

func (s strictStandIn) CancelSandboxReset(context.Context, uint64) (store.RuntimeDeploymentView, error) {
	s.unexpected("CancelSandboxReset")
	return store.RuntimeDeploymentView{}, nil
}
