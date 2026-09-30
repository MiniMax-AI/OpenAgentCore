package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Dependencies is everything the handler uses. Each application area is one
// field typed as an interface declared next to the area's handlers, listing
// exactly the methods they call. Every field is required unless its comment
// says what nil means; NewHandler rejects a missing one.
type Dependencies struct {
	// Engine is the default Harness. Harnesses lists the other Harnesses this
	// deployment enables for explicit selection.
	Engine    string
	Harnesses []string
	// Policy is the immutable qualification shared with the execution
	// Dispatcher. The zero value uses the built-in engine registrations.
	Policy execution.Policy
	// CoreKeys authenticates /core/v1 and keeps Core keys out of Project key
	// authentication.
	CoreKeys *DeploymentAuthenticator
	// Installation holds the facts GET /core/v1/installation reports;
	// InstallationBindings counts what is bound to the current public URL.
	Installation         Installation
	InstallationBindings InstallationBindings

	Projects             Projects
	Vaults               Vaults
	VaultsReader         VaultsReader
	ModelProviders       ModelProviders
	Files                Files
	FilesReader          FilesReader
	Skills               Skills
	EnvironmentTemplates EnvironmentTemplates
	Agents               Agents
	AgentsReader         AgentsReader
	Sessions             Sessions
	SessionEvents        SessionEvents
	SessionHistory       SessionHistory
	Subagents            Subagents
	Artifacts            Artifacts
	SessionAdmin         SessionAdmin
	Environments         Environments
	ExecutorConnections  ExecutorConnections
	Admin                Admin
	AdminAudit           AdminAudit
	WriteAudit           WriteAudit
	Metrics              Metrics
	RuntimeObservations  RuntimeObservations
	RuntimeHistory       RuntimeHistory

	// Execution is nil when this Core runs without a Runtime gateway, and so
	// without an execution Worker. Work that needs one then answers 503
	// execution_unavailable.
	Execution *Execution
	// Sandboxes is nil when this Core has no managed sandbox installation. The
	// sandbox node and manager routes are then absent, and openai_hosted
	// Sessions answer 503 execution_unavailable. It requires Execution.
	Sandboxes *Sandboxes
}

// Execution is the execution Worker's surface. Every field is required unless
// its comment says what nil means.
type Execution struct {
	// ExecutorURL is the validated daemon WebSocket URL that self-hosted
	// Sessions report and executors connect to.
	ExecutorURL    string
	Admission      Admission
	SessionArchive SessionArchive
	Workspaces     EnvironmentWorkspaces
	// NativeInstaller is nil for a build without a source revision: the native
	// installation routes are then absent and Sessions carry no installation.
	NativeInstaller *NativeInstaller
}

// Sandboxes is the managed sandbox deployment's surface. Every field is
// required.
type Sandboxes struct {
	Deployment             Deployment
	DeploymentChanges      DeploymentChanges
	ConfigurationDiscovery ConfigurationDiscovery
}

type Handler struct {
	Dependencies
	harnesses map[string]bool
}

// NewHandler builds the HTTP handler from complete dependencies.
func NewHandler(deps Dependencies) (http.Handler, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	deps.Installation.Object = "core.installation"
	h := &Handler{Dependencies: deps, harnesses: make(map[string]bool, len(deps.Harnesses))}
	for _, kind := range deps.Harnesses {
		h.harnesses[kind] = true
	}
	return CanonicalPaths(h.routes()), nil
}

func (d Dependencies) validate() error {
	if !store.ValidEngine(d.Engine) {
		return errors.New("api: a valid default Harness is required")
	}
	if d.CoreKeys == nil {
		return errors.New("api: CoreKeys is required")
	}
	if err := required(
		field{"InstallationBindings", d.InstallationBindings}, field{"Projects", d.Projects},
		field{"Vaults", d.Vaults}, field{"VaultsReader", d.VaultsReader},
		field{"ModelProviders", d.ModelProviders}, field{"Skills", d.Skills},
		field{"Files", d.Files}, field{"FilesReader", d.FilesReader},
		field{"Agents", d.Agents}, field{"AgentsReader", d.AgentsReader},
		field{"EnvironmentTemplates", d.EnvironmentTemplates}, field{"Sessions", d.Sessions},
		field{"SessionEvents", d.SessionEvents}, field{"SessionHistory", d.SessionHistory}, field{"Subagents", d.Subagents},
		field{"Artifacts", d.Artifacts}, field{"SessionAdmin", d.SessionAdmin}, field{"Environments", d.Environments},
		field{"ExecutorConnections", d.ExecutorConnections}, field{"Admin", d.Admin}, field{"AdminAudit", d.AdminAudit}, field{"WriteAudit", d.WriteAudit},
		field{"Metrics", d.Metrics}, field{"RuntimeObservations", d.RuntimeObservations}, field{"RuntimeHistory", d.RuntimeHistory},
	); err != nil {
		return err
	}
	if e := d.Execution; e != nil {
		if e.ExecutorURL == "" {
			return errors.New("api: Execution.ExecutorURL is required")
		}
		if e.NativeInstaller != nil && e.NativeInstaller.Version == "" {
			return errors.New("api: Execution.NativeInstaller.Version is required")
		}
		if err := required(field{"Execution.Admission", e.Admission}, field{"Execution.SessionArchive", e.SessionArchive}, field{"Execution.Workspaces", e.Workspaces}); err != nil {
			return err
		}
	}
	if s := d.Sandboxes; s != nil {
		if d.Execution == nil {
			return errors.New("api: Sandboxes requires Execution")
		}
		return required(field{"Sandboxes.Deployment", s.Deployment}, field{"Sandboxes.DeploymentChanges", s.DeploymentChanges}, field{"Sandboxes.ConfigurationDiscovery", s.ConfigurationDiscovery})
	}
	return nil
}

type field struct {
	name  string
	value any
}

func required(fields ...field) error {
	for _, f := range fields {
		if f.value == nil {
			return fmt.Errorf("api: %s is required", f.name)
		}
	}
	return nil
}

// executorURL is the daemon URL self-hosted Sessions report, or empty when
// this Core cannot execute.
func (h *Handler) executorURL() string {
	if h.Execution == nil {
		return ""
	}
	return h.Execution.ExecutorURL
}
