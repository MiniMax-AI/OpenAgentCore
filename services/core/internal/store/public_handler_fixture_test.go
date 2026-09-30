package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/agentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/skillpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/templatepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// testExecutorURL is the daemon URL self-hosted Sessions report unless a test
// sets another with executorURL.
const testExecutorURL = "wss://core.example/api/v1/agent-daemon/ws"

// publicHandler serves s through api.NewHandler. s backs every area the Store
// implements, and db is the database and credential key that built s; the
// audit reads, Agents, Files, Vaults, Items, Subagents and Artifacts come from
// db. keys authenticate as Project keys and "admin" as the Core key. Metrics,
// Runtime observation and history, and executor connections are strict
// stand-ins. Execution and Sandboxes stay disabled unless configure sets them.
func publicHandler(t testing.TB, s *store.Store, db fixtureDB, keys fixtureKeyResolver, engine string, configure ...func(*api.Dependencies)) (http.Handler, error) {
	t.Helper()
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("admin")})
	if err != nil {
		return nil, err
	}
	strict := strictStandIn{t}
	audit := auditpg.New(pgunit.NewPool(db.pool))
	agentStore, agentService := fixtureAgents(t, db)
	fileStore, fileService := fixtureFiles(t, db)
	vaultStore, vaultService, err := fixtureVaults(db)
	if err != nil {
		return nil, err
	}
	templates := templatepg.New(pgunit.NewPool(db.pool), db.cipher)
	environmentTemplates, err := environmenttemplates.NewService(templates)
	if err != nil {
		return nil, err
	}
	modelConfigurationStore := modelconfigurationpg.New(pgunit.NewPool(db.pool), db.cipher)
	modelConfigurationService, err := modelconfiguration.NewService(modelConfigurationStore)
	if err != nil {
		return nil, err
	}
	skillStore := skillpg.New(pgunit.NewPool(db.pool), db.cipher)
	skillService, err := skills.NewService(skillStore, skillStore)
	if err != nil {
		return nil, err
	}
	projectStore, projectService := fixtureProjects(t, db)
	sessionStore, sessionService, err := fixtureSessions(db)
	if err != nil {
		return nil, err
	}
	deps := api.Dependencies{
		Engine: engine, CoreKeys: admin, InstallationBindings: fixtureDeployment(t, db),
		Projects: projectService, ProjectsReader: fixtureProjectsReader{Reader: projectStore, keys: keys},
		ModelProviders: modelConfigurationService, ModelProvidersReader: modelConfigurationStore,
		Vaults: vaultService, VaultsReader: vaultStore,
		Files: fileService, FilesReader: fileStore,
		EnvironmentTemplates: environmentTemplates, EnvironmentTemplatesReader: templates,
		Skills: skillService, SkillsReader: skillStore,
		Agents: agentService, AgentsReader: agentStore,
		Sessions:        s,
		SessionCreation: s,
		SessionEvents:   s,
		Turns:           s,
		Items:           sessionStore,
		Subagents:       sessionStore,
		Artifacts:       sessionService,
		ArtifactsReader: sessionStore,
		SessionAdmin:    s, Environments: s, Admin: s, AdminAudit: audit, WriteAudit: audit,
		ExecutorConnections: strict, Metrics: strict, RuntimeObservations: strict, RuntimeHistory: strict,
	}
	for _, c := range configure {
		c(&deps)
	}
	return api.NewHandler(deps)
}

// fixtureAgents builds the Agent adapter and service on db.
func fixtureAgents(t testing.TB, db fixtureDB) (*agentpg.Store, *agents.Service) {
	t.Helper()
	agentStore := agentpg.New(pgunit.NewPool(db.pool), db.cipher)
	agentService, err := agents.NewService(agentStore)
	if err != nil {
		t.Fatal(err)
	}
	return agentStore, agentService
}

// fixtureFiles builds the File adapter and service on db.
func fixtureFiles(t testing.TB, db fixtureDB) (*filepg.Store, *files.Service) {
	t.Helper()
	fileStore := filepg.New(pgunit.NewPool(db.pool))
	fileService, err := files.NewService(fileStore)
	if err != nil {
		t.Fatal(err)
	}
	return fileStore, fileService
}

// storeKeys resolves Project keys from the database, as production does.
func storeKeys(*store.Store) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.ProjectsReader = d.ProjectsReader.(fixtureProjectsReader).Reader }
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
		d.Execution = &api.Execution{
			ExecutorURL:      testExecutorURL,
			SessionAdmission: s,
			InputAdmission:   s,
			SessionArchive:   strictStandIn{t},
			Workspaces:       strictStandIn{t},
		}
	}
}

// workerExecution runs Sessions through worker.
func workerExecution(worker *execution.Worker) func(*api.Dependencies) {
	return func(d *api.Dependencies) {
		d.Execution = &api.Execution{
			ExecutorURL:      testExecutorURL,
			SessionAdmission: worker,
			InputAdmission:   worker,
			SessionArchive:   worker,
			Workspaces:       worker,
		}
	}
}

// executorURL replaces the daemon URL self-hosted Sessions report. It follows
// the option that enables Execution.
func executorURL(url string) func(*api.Dependencies) {
	return func(d *api.Dependencies) { d.Execution.ExecutorURL = url }
}

// managedSandboxes enables the managed sandbox deployment on db: its
// administration and node routes and openai_hosted Environments. Deployment
// changes, reset and discovery need the Worker and are strict stand-ins. It
// follows the option that enables Execution.
func managedSandboxes(t testing.TB, s *store.Store, db fixtureDB) func(*api.Dependencies) {
	return func(d *api.Dependencies) {
		d.Sandboxes = &api.Sandboxes{
			Deployment:             fixtureDeployment(t, db),
			NodeAllocations:        s,
			DeploymentChanges:      strictStandIn{t},
			DeploymentReset:        strictStandIn{t},
			ConfigurationDiscovery: strictStandIn{t},
		}
	}
}

// modelProviderDefaults resolves deployment model provider defaults with
// resolve instead of the stored deployment configuration.
func modelProviderDefaults(resolve func(context.Context, string) (*modelconfiguration.Snapshot, error)) func(*api.Dependencies) {
	return func(d *api.Dependencies) {
		d.ModelProviders = resolvedModelProviders{ModelProviders: d.ModelProviders, resolve: resolve}
	}
}

type resolvedModelProviders struct {
	api.ModelProviders
	resolve func(context.Context, string) (*modelconfiguration.Snapshot, error)
}

func (p resolvedModelProviders) Resolve(ctx context.Context, harness string) (*modelconfiguration.Snapshot, error) {
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

func (s strictStandIn) ArchiveManagedSession(context.Context, string, string, uint64) (sessions.ManagedArchive, error) {
	s.unexpected("ArchiveManagedSession")
	return sessions.ManagedArchive{}, nil
}

func (s strictStandIn) ReadEnvironmentDirectory(context.Context, sessions.Environment, string) (proto.WorkspaceDirectoryResult, error) {
	s.unexpected("ReadEnvironmentDirectory")
	return proto.WorkspaceDirectoryResult{}, nil
}

func (s strictStandIn) WriteEnvironmentFile(context.Context, sessions.Environment, string, []byte) (int64, error) {
	s.unexpected("WriteEnvironmentFile")
	return 0, nil
}

func (s strictStandIn) DiscoverConfiguration(context.Context, string, sandbox.ConfigurationDiscoveryInput) (json.RawMessage, error) {
	s.unexpected("DiscoverConfiguration")
	return nil, nil
}

func (s strictStandIn) InitializeSandboxDeployment(context.Context, sandbox.Selection) (deployment.View, error) {
	s.unexpected("InitializeSandboxDeployment")
	return deployment.View{}, nil
}

func (s strictStandIn) UpdateSandboxDeployment(context.Context, sandbox.Selection) (deployment.View, error) {
	s.unexpected("UpdateSandboxDeployment")
	return deployment.View{}, nil
}

func (s strictStandIn) StartSandboxReset(context.Context, deployment.ResetRequest) (deployment.View, error) {
	s.unexpected("StartSandboxReset")
	return deployment.View{}, nil
}

func (s strictStandIn) CancelSandboxReset(context.Context, uint64) (deployment.View, error) {
	s.unexpected("CancelSandboxReset")
	return deployment.View{}, nil
}
