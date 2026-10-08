package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
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
	// CoreKeys authenticates /core/v1 and keeps Core keys out of Project key
	// authentication.
	CoreKeys *DeploymentAuthenticator
	// Installation holds the facts GET /core/v1/installation reports;
	// InstallationBindings counts what is bound to the current public URL.
	Installation         Installation
	InstallationBindings InstallationBindings
	// Distribution is nil when no matched installation artifacts are installed.
	Distribution *nativeinstaller.Catalog

	Projects             Projects
	ProjectsReader       ProjectsReader
	Vaults               Vaults
	VaultsReader         VaultsReader
	ModelProviders       ModelProviders
	ModelProvidersReader ModelProvidersReader
	Files                Files
	FilesReader          FilesReader
	Skills               Skills
	SkillsReader         SkillsReader
	EnvironmentTemplates EnvironmentTemplates
	Agents               Agents
	AgentsReader         AgentsReader
	Sessions             Sessions
	SessionsReader       SessionsReader
	SessionCreation      SessionCreation
	SessionEvents        SessionEvents
	Turns                Turns
	Items                Items
	Subagents            Subagents
	Artifacts            Artifacts
	ArtifactsReader      ArtifactsReader
	SessionAdmin         SessionAdmin
	Environments         Environments
	EnvironmentsReader   EnvironmentsReader
	ExecutorConnections  ExecutorConnections
	Admin                Admin
	AdminAudit           AdminAudit
	WriteAudit           WriteAudit
	Metrics              Metrics
	RuntimeObservations  RuntimeObservations
	RuntimeHistory       RuntimeHistory

	EnvironmentTemplatesReader EnvironmentTemplatesReader

	Execution Execution
	Sandboxes Sandboxes
}

// Execution is the execution Worker's surface. Every field is required unless
// its comment says what nil means.
type Execution struct {
	// ExecutorURL is the validated daemon WebSocket URL that self-hosted
	// Sessions report and executors connect to.
	ExecutorURL      string
	SessionAdmission SessionAdmission
	InputAdmission   InputAdmission
	SessionArchive   SessionArchive
	Workspaces       EnvironmentWorkspaces
	// Links is the Link relay, served at GET /api/v1/sandbox-link.
	Links http.Handler
	// Machine handlers authenticate their own callers.
	Bootstrap, RuntimeConnect, Enrollment, Connection http.Handler
	// NativeInstaller is nil for a build without a source revision: the native
	// installation routes are then absent and Sessions carry no installation.
	NativeInstaller *NativeInstaller
}

// Sandboxes is the managed sandbox deployment's surface. Every field is
// required.
type Sandboxes struct {
	NodeConnect            http.Handler
	Deployment             Deployment
	NodeAllocations        NodeAllocations
	DeploymentChanges      DeploymentChanges
	DeploymentReset        DeploymentReset
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
	if !sessions.ValidEngine(d.Engine) {
		return errors.New("api: a valid default Harness is required")
	}
	if d.CoreKeys == nil {
		return errors.New("api: CoreKeys is required")
	}
	if err := required(
		field{"InstallationBindings", d.InstallationBindings},
		field{"Projects", d.Projects}, field{"ProjectsReader", d.ProjectsReader},
		field{"Vaults", d.Vaults}, field{"VaultsReader", d.VaultsReader},
		field{"ModelProviders", d.ModelProviders}, field{"ModelProvidersReader", d.ModelProvidersReader},
		field{"Files", d.Files}, field{"FilesReader", d.FilesReader},
		field{"EnvironmentTemplates", d.EnvironmentTemplates}, field{"EnvironmentTemplatesReader", d.EnvironmentTemplatesReader},
		field{"Skills", d.Skills}, field{"SkillsReader", d.SkillsReader},
		field{"Agents", d.Agents}, field{"AgentsReader", d.AgentsReader},
		field{"Sessions", d.Sessions}, field{"SessionsReader", d.SessionsReader},
		field{"SessionCreation", d.SessionCreation},
		field{"SessionEvents", d.SessionEvents},
		field{"Turns", d.Turns},
		field{"Items", d.Items},
		field{"Subagents", d.Subagents},
		field{"Artifacts", d.Artifacts},
		field{"ArtifactsReader", d.ArtifactsReader},
		field{"SessionAdmin", d.SessionAdmin}, field{"Environments", d.Environments}, field{"EnvironmentsReader", d.EnvironmentsReader},
		field{"ExecutorConnections", d.ExecutorConnections}, field{"Admin", d.Admin}, field{"AdminAudit", d.AdminAudit}, field{"WriteAudit", d.WriteAudit},
		field{"Metrics", d.Metrics}, field{"RuntimeObservations", d.RuntimeObservations}, field{"RuntimeHistory", d.RuntimeHistory},
	); err != nil {
		return err
	}
	e, s := d.Execution, d.Sandboxes
	if e.ExecutorURL == "" {
		return errors.New("api: Execution.ExecutorURL is required")
	}
	if e.NativeInstaller != nil && (e.NativeInstaller.Version == "" || e.NativeInstaller.Base == "") {
		return errors.New("api: Execution.NativeInstaller.Version and Base are required")
	}
	return required(
		field{"Execution.SessionAdmission", e.SessionAdmission},
		field{"Execution.InputAdmission", e.InputAdmission},
		field{"Execution.SessionArchive", e.SessionArchive},
		field{"Execution.Workspaces", e.Workspaces},
		field{"Execution.Links", e.Links},
		field{"Execution.Bootstrap", e.Bootstrap}, field{"Execution.RuntimeConnect", e.RuntimeConnect},
		field{"Execution.Enrollment", e.Enrollment}, field{"Execution.Connection", e.Connection},
		field{"Sandboxes.NodeConnect", s.NodeConnect},
		field{"Sandboxes.Deployment", s.Deployment},
		field{"Sandboxes.NodeAllocations", s.NodeAllocations},
		field{"Sandboxes.DeploymentChanges", s.DeploymentChanges},
		field{"Sandboxes.DeploymentReset", s.DeploymentReset},
		field{"Sandboxes.ConfigurationDiscovery", s.ConfigurationDiscovery},
	)
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
