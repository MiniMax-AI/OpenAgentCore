// Package sandbox defines the Sandbox Provider contract for authorized compute.
// This file is the Core–Sandbox Provider protocol; docs/sandbox-provider.md
// describes it. Start here when adding an adapter. Value types that Core also
// uses beyond this boundary, such as DeploymentSpec and CallFence, live in their
// own files of this package.
// Core owns durable Environment/allocation state and lifecycle serialization;
// SandboxProvider owns compute and bootstrap, Runtime owns capability preparation,
// and Harness adapters own native execution. Compute running is not execution ready.
//
// The protocol has two interfaces. SandboxProvider is the allocation-time half
// and ConfigurationAdapter the setup-time half. Every adapter implements every
// method of both and declares which operations it supports: SandboxProvider
// through ProviderOperations, ConfigurationAdapter through Requirements. An
// unsupported method returns a typed providercontract.UnsupportedError; method-set
// presence never means support.
//
// Registration is explicit construction, not a global init-time registry. Adapters
// register in sandbox/providers; Core's managed setup constructs direct adapters
// or node proxies. execution.RuntimeProvider binds the selected adapter to
// installation, backend, deployment generation and node identity. Keep vendor
// configuration at those construction boundaries; common lifecycle code selects
// behavior through these contracts, never through a vendor name.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

var (
	ErrInvalid = errors.New("invalid sandbox configuration")
	// ErrWorkspaceRequired reports a Provider that runs every Environment on
	// external storage while none is selected.
	ErrWorkspaceRequired  = errors.New("sandbox provider requires workspace storage")
	ErrOwnership          = errors.New("sandbox ownership mismatch")
	ErrExists             = errors.New("sandbox allocation already exists")
	ErrNotFound           = errors.New("sandbox allocation not found")
	ErrCommandUnconfirmed = errors.New("initialization command outcome unconfirmed; reclaim allocation before reuse")
	// ErrComputeUnconfirmed requires observation of the retained operation identity;
	// it does not authorize another Create, capture, restore, or cold start.
	ErrComputeUnconfirmed = errors.New("sandbox lifecycle outcome unconfirmed")
	// ErrCheckpointUnusable is a Resume outcome: the adapter durably closed this
	// operation before dispatching native restoration, and the snapshot cannot be
	// restored. It repeats for the same operation and is never returned once
	// native restoration was dispatched.
	ErrCheckpointUnusable = errors.New("sandbox checkpoint cannot be restored")
)

// Reference must be persisted by the caller before Create. AllocationID is a fresh
// UUID for one attempt, not the Environment ID. Serialize lifecycle operations for
// an allocation; a lost Create response is resolved with GetInfo, never by replay.
type Reference struct{ TenantID, EnvironmentID, AllocationID string }

type Bootstrap struct {
	Workspace *workspacefs.Binding `json:",omitempty"`
	Reference
	SessionID, DeviceID, CoreURL, Credential string
	// SuspendControl is true exactly when the Provider declares checkpoint support.
	SuspendControl bool
}

// Info describes compute only. Running does not establish daemon authentication,
// native preparation, Environment readiness or a renewable provider lease.
type Info struct {
	Reference
	ProviderID, State string
	// BootstrapComplete is provider evidence that initialization has reached its
	// last mutating step. It does not establish daemon or native readiness.
	BootstrapComplete bool
	// CreateSettled proves that the original create and initialization attempt can
	// no longer mutate resources. An absent observation needs this explicit proof;
	// an ordinary missing resource or empty provider listing is not sufficient.
	CreateSettled bool
}
type Command struct {
	Args      []string
	Directory string
	// Stdin carries confidential initialization bytes without exposing them in argv.
	Stdin []byte
}

const MaxCommandInputBytes = 50*1024*1024 + 32

type CommandResult struct {
	Stdout, Stderr string
	ExitCode       int
}

// SandboxProvider manages one persisted Reference at a time. Every call has a
// bounded context; cancellation ends the caller's wait, not proof of native stop.
// Non-nil errors retain ownership, including partial results. Do not retry Create
// or RunCommand after unknown delivery; observe/reclaim the original Reference.
// Implementations verify installation plus Reference ownership before mutation.
// See docs/sandbox-provider.md for settlement, cleanup and retry requirements.
type SandboxProvider interface {
	providercontract.Declared
	// Create performs the original attempt once; no credential overwrite on conflict.
	Create(context.Context, Bootstrap) (Info, error)
	// GetInfo observes compute without creating, starting, renewing or preparing it.
	GetInfo(context.Context, Reference) (Info, error)
	// Renew extends an existing native lease where supported; otherwise it observes.
	// It never revives stopped compute or establishes execution readiness.
	Renew(context.Context, Reference) (Info, error)
	// Kill confirms removal of owned compute and retained resources. Absence is
	// idempotent, but nil alone cannot settle an outstanding Create.
	Kill(context.Context, Reference) error
	RunCommand(context.Context, Reference, Command) (CommandResult, error)

	// The checkpoint lifecycle supplies exact-incarnation operations; Worker and
	// Store remain the lifecycle owner. Its operations share one declaration.
	Initial(context.Context, Reference) (Compute, error)
	NewCompute(context.Context, Reference, uint64, *SnapshotIdentity) (Compute, error)
	// GetCompute returns ErrNotFound only after the owning node verified that
	// the exact incarnation no longer exists.
	GetCompute(context.Context, Reference, Compute) (ComputeState, error)
	Suspend(context.Context, SuspendRequest) (ComputeState, error)
	Resume(context.Context, ResumeRequest) (ComputeState, error)
	KillCompute(context.Context, Reference, Compute) error
	DeleteSnapshot(context.Context, Reference, SnapshotIdentity) error
	RunCommandCompute(context.Context, Reference, Compute, Command) (CommandResult, error)
	// ResumeCompute thaws only the same resident instance after an aborted pause.
	ResumeCompute(context.Context, Reference, Compute) (ComputeState, error)
	// RenewCompute extends the native lease of the exact running incarnation
	// where the Provider has one, and otherwise observes exactly like
	// GetCompute. It never revives stopped compute.
	RenewCompute(context.Context, Reference, Compute) (ComputeState, error)

	// The preparation cache keeps the Environment filesystem left by one finished
	// preparation. Key is opaque and the adapter owns storage and lifetime. Both
	// address the allocation's initial compute and share one declaration.
	// RestorePreparation returns false when it holds no valid entry.
	RestorePreparation(ctx context.Context, r Reference, key string) (bool, error)
	SavePreparation(ctx context.Context, r Reference, key string) error

	// Observe reads one owned Runtime instance after verifying its ownership. It
	// never renews, restarts or stops compute.
	Observe(context.Context, runtimeobs.Target) (runtimeobs.Sample, error)
}

// requiredOperations are supported by every Provider.
var requiredOperations = []string{"Create", "GetInfo", "Renew", "Kill", "RunCommand"}

// checkpointOperations are all supported or all unsupported: partial cleanup or
// restore support cannot safely own a compute incarnation.
var checkpointOperations = []string{"Initial", "NewCompute", "GetCompute", "Suspend", "Resume", "KillCompute", "DeleteSnapshot", "RunCommandCompute", "ResumeCompute", "RenewCompute"}

// preparationOperations are both supported or both unsupported.
var preparationOperations = []string{"RestorePreparation", "SavePreparation"}

// Compute identifies one incarnation of an allocation. Name is provider-derived.
// ID is empty only until the original create or restore result is observed.
type Compute struct {
	Generation   uint64
	Name         string
	ID           string
	RestoredFrom *SnapshotIdentity
}

// CheckpointCompatibility identifies an adapter-verified private checkpoint domain
// and execution compatibility class. Core compares these opaque tokens only.
type CheckpointCompatibility struct {
	ArtifactDomain string `json:"artifact_domain"`
	ExecutionClass string `json:"execution_class"`
}

func (c CheckpointCompatibility) Validate() error {
	if c.ArtifactDomain == "" || c.ExecutionClass == "" || len(c.ArtifactDomain) > 256 || len(c.ExecutionClass) > 256 {
		return ErrInvalid
	}
	return nil
}

// MaxSnapshotEvidenceBytes bounds SnapshotIdentity.Evidence.
const MaxSnapshotEvidenceBytes = 4096

// SnapshotIdentity is provider evidence from a verified full snapshot. Core
// persists it unchanged and records consumption separately; it never invents
// paths, checksums, native checkpoint fields, or source identity.
// Evidence is the Provider's private verification evidence: Core and the node
// proxy store and return it unchanged and never interpret it.
type SnapshotIdentity struct {
	Compatibility    CheckpointCompatibility
	Reference        string
	ID               string
	Evidence         string
	OperationID      string
	SourceGeneration uint64
	SourceName       string
	SourceID         string
}

// Validate checks the fields Core relies on.
func (s SnapshotIdentity) Validate() error {
	if len(s.Evidence) > MaxSnapshotEvidenceBytes {
		return ErrInvalid
	}
	return s.Compatibility.Validate()
}

type ComputeState struct {
	Compute Compute
	// Status is the native state of Compute. "stopped" and "crashed" mean the
	// adapter verified that the exact incarnation's process no longer exists;
	// its record or disks can remain until KillCompute removes them.
	Status            string
	BootstrapComplete bool
	Snapshot          *SnapshotIdentity
	// SourceStopped proves the checkpoint is durable and all source-local compute
	// and capture obligations have settled, so another node can own restoration.
	SourceStopped bool
	// RestoreAttemptClosed echoes the exact Resume OperationID only after a
	// never-dispatched attempt is durably closed against late native execution.
	RestoreAttemptClosed string
}

// Terminated reports a verified stopped or crashed incarnation.
func (s ComputeState) Terminated() bool { return s.Status == "stopped" || s.Status == "crashed" }

// ClosesRestoreAttempt validates a never-executed closure against the exact
// persisted request. Absence without this operation-bound evidence is unknown.
func (s ComputeState) ClosesRestoreAttempt(q ResumeRequest) bool {
	return q.ObserveOnly && q.OperationID != "" && s.RestoreAttemptClosed == q.OperationID && s.Status == "absent" &&
		s.Compute.ID == "" && q.Target.ID == "" && s.Compute.Name == q.Target.Name && s.Compute.Generation == q.Target.Generation &&
		s.Compute.RestoredFrom != nil && q.Target.RestoredFrom != nil && *s.Compute.RestoredFrom == q.Snapshot && *q.Target.RestoredFrom == q.Snapshot &&
		!s.BootstrapComplete && !s.SourceStopped && s.Snapshot == nil
}

type SuspendRequest struct {
	Reference   Reference
	OperationID string
	Source      Compute
	Snapshot    *SnapshotIdentity
	// Recovery observes the previous attempt and never starts a new capture.
	ObserveOnly bool
}
type ResumeRequest struct {
	Workspace   *workspacefs.Binding `json:",omitempty"`
	Reference   Reference
	OperationID string
	Snapshot    SnapshotIdentity
	Target      Compute
	// Recovery observes the previous target and never starts a new restore.
	ObserveOnly bool
}

// ValidateProvider checks a constructed Provider's declaration.
func ValidateProvider(p SandboxProvider) error {
	if p == nil {
		return providercontract.ErrContract
	}
	if value := reflect.ValueOf(p); value.Kind() == reflect.Pointer && value.IsNil() {
		return providercontract.ErrContract
	}
	return ValidateOperations(p.ProviderOperations())
}

// ValidateOperations rejects omitted, unknown and contradictory declarations.
// SandboxProvider's method set is the operation inventory.
func ValidateOperations(operations providercontract.Operations) error {
	contract := reflect.TypeFor[SandboxProvider]()
	for i := range contract.NumMethod() {
		name := contract.Method(i).Name
		if name == "ProviderOperations" {
			continue
		}
		if err := operations[name].Check(name); err != nil && !errors.Is(err, providercontract.ErrUnsupported) {
			return err
		}
	}
	for name := range operations {
		if _, known := contract.MethodByName(name); !known || name == "ProviderOperations" {
			return fmt.Errorf("%w: unknown operation %s", providercontract.ErrContract, name)
		}
	}
	for _, name := range requiredOperations {
		if operations[name].State != providercontract.Supported {
			return fmt.Errorf("%w: required operation %s", providercontract.ErrContract, name)
		}
	}
	for _, name := range checkpointOperations {
		if (operations[name].State == providercontract.Supported) != SupportsCheckpoint(operations) {
			return fmt.Errorf("%w: incomplete checkpoint lifecycle", providercontract.ErrContract)
		}
	}
	if operations[preparationOperations[0]].State != operations[preparationOperations[1]].State {
		return fmt.Errorf("%w: incomplete preparation cache", providercontract.ErrContract)
	}
	return nil
}

// SupportsCheckpoint is the one predicate over a declaration: the checkpoint
// operations are all supported or all unsupported, so Initial decides.
func SupportsCheckpoint(operations providercontract.Operations) bool {
	return operations["Initial"].State == providercontract.Supported
}

// ProcessPaths locates installed adapter helpers and their private state. The
// launcher supplies roots from the distribution layout; adapters own subpaths.
type ProcessPaths struct {
	ArtifactRoot string
	StateRoot    string
}

// Requirement has no implicit default: registration must choose either value.
type Requirement string

const (
	Required    Requirement = "required"
	NotRequired Requirement = "not_required"
)

// ConfigurationRequirements is the setup-time declaration. Discovery,
// SelectionDiscovery and CredentialVerification declare DiscoverConfiguration,
// DiscoverSelection and VerifyCredential; requiring a credential does not
// promise VerifyCredential.
type ConfigurationRequirements struct {
	Credential             Requirement
	PublicOrigin           Requirement
	Discovery              providercontract.Support
	SelectionDiscovery     providercontract.Support
	CredentialVerification providercontract.Support
}

// Text is operator-facing copy in every supported locale.
type Text struct {
	EN   string `json:"en"`
	ZHCN string `json:"zh-CN"`
}

func (t Text) valid() bool { return t.EN != "" && t.ZHCN != "" }

// SetupField is one value an operator enters to connect the Provider. A Secret
// field is submitted as the credential, any other as configuration. An Optional
// field may stay empty and the Provider supplies its default. A Discovered field
// takes its value from DiscoverConfiguration. Help may be empty in both locales.
type SetupField struct {
	Name       string `json:"name"`
	Secret     bool   `json:"secret"`
	Optional   bool   `json:"optional"`
	Discovered bool   `json:"discovered"`
	Label      Text   `json:"label"`
	Help       Text   `json:"help"`
}

// SetupPreset is a named set of field values, such as one hosted service.
// CredentialURL, when set, is where the operator creates the credential.
type SetupPreset struct {
	Label         string            `json:"label"`
	Values        map[string]string `json:"values"`
	CredentialURL string            `json:"credential_url,omitempty"`
}

// SetupDeclaration is everything the console needs to set up one Provider.
// Label is the Provider's proper noun. Cautions are risks an operator confirms
// before choosing it. Recommended marks the default among Providers of one
// deployment mode, and at most one does.
type SetupDeclaration struct {
	Label       string        `json:"label"`
	Summary     Text          `json:"summary"`
	Cautions    []Text        `json:"cautions"`
	Recommended bool          `json:"recommended"`
	Fields      []SetupField  `json:"fields"`
	Presets     []SetupPreset `json:"presets"`
}

// Validate checks the declaration against the Provider's configuration
// requirements: secret fields exist exactly when a credential is required,
// discovered fields exactly when discovery is supported, and every text has
// both locales.
func (d SetupDeclaration) Validate(r ConfigurationRequirements) error {
	secret, discovered := false, false
	ok := d.Label != "" && d.Summary.valid()
	names := map[string]bool{}
	for _, f := range d.Fields {
		ok = ok && f.Name != "" && !names[f.Name] && f.Label.valid() && (f.Help == Text{} || f.Help.valid())
		names[f.Name] = true
		secret, discovered = secret || f.Secret, discovered || f.Discovered
		ok = ok && !(f.Secret && (f.Optional || f.Discovered))
	}
	for _, c := range d.Cautions {
		ok = ok && c.valid()
	}
	for _, p := range d.Presets {
		ok = ok && p.Label != ""
		for name := range p.Values {
			ok = ok && names[name]
		}
	}
	if !ok || secret != (r.Credential == Required) || discovered != (r.Discovery.State == providercontract.Supported) {
		return ErrInvalid
	}
	return nil
}

// DeploymentPolicy is the deployment declaration. Workspace, when present, means
// the Provider runs every Environment on external storage; absent means its own
// disk. Disk declares independent disk limits. DefaultResources is the size setup
// proposes, or nil when the Provider's configuration selects it.
type DeploymentPolicy struct {
	Workspace        *workspacefs.Requirements `json:"workspace,omitempty"`
	Disk             bool                      `json:"disk"`
	DefaultResources *Resources                `json:"default_resources"`
}

// Configuration is an adapter-owned typed value, never a request or response DTO.
// Implementations must exclude secrets from JSON and safe diagnostic output.
type Configuration interface {
	HasCredential() bool
	ReplacesCredential() bool
}

// ConfigurationRecord separates public selectors, read-only observations and
// secret bytes. Store encrypts Secret with the installation and generation.
// Only adapter codecs may produce Public and Metadata; neither is input passthrough.
type ConfigurationRecord struct {
	Public   json.RawMessage
	Metadata json.RawMessage
	Secret   []byte `json:"-"`
}

// Selection is the typed deployment configuration shared by preview and commit.
type Selection struct {
	DeploymentSpec
	ExpectedGeneration uint64        `json:"expected_generation"`
	Provider           string        `json:"provider"`
	Configuration      Configuration `json:"-"`
}

func (s Selection) HasCredential() bool {
	return s.Configuration != nil && s.Configuration.HasCredential()
}

func (s Selection) ReplacesCredential() bool {
	return s.Configuration != nil && s.Configuration.ReplacesCredential()
}

// DirectConfig constructs a direct adapter for one selection. Fence counts the
// adapter's helper processes so that a credential commit waits for them.
type DirectConfig struct {
	ProcessPaths   ProcessPaths
	InstallationID string
	Selection      Selection
	Fence          *CallFence
}

// NodeConfig is a node's configuration for one deployment generation. Native
// holds only the selected adapter's node-local settings, such as host paths;
// that adapter alone decodes it, strictly. Resources are read from
// Specification, never copied into Native.
type NodeConfig struct {
	Specification  DeploymentSpec  `json:"specification"`
	Generation     uint64          `json:"generation"`
	CoreURL        string          `json:"core_url"`
	Provider       string          `json:"provider"`
	InstallationID string          `json:"installation_id"`
	Native         json.RawMessage `json:"native"`
}

// LocalOptions supplies process-local context without changing persisted configuration.
type LocalOptions struct {
	Workspace workspacefs.Resolver
	// Standalone selects registration or execution without a generation manager.
	Standalone bool
	// GenerationStateDirectory is the node state directory when constructing a
	// retained generation. It must be canonical and absolute, and Standalone
	// must be false.
	GenerationStateDirectory string
}

// Built is a constructed node adapter. Construction fills InstallationID and
// SpecificationDigest; the adapter fills the rest.
type Built struct {
	SpecificationDigest                string
	Provider                           SandboxProvider
	InstallationID, BackendFingerprint string
	Probe                              func(context.Context) (*CheckpointCompatibility, error)
	// Quiescent is nil when no helper can outlive its caller.
	Quiescent func() bool
}

// ConfigurationDiscoveryQuery selects the options of one Discovered field. Path
// holds the values chosen at earlier levels, empty for the first level.
type ConfigurationDiscoveryQuery struct {
	Field string   `json:"field"`
	Path  []string `json:"path"`
}

// ConfigurationDiscoveryInput is a transient read-only request. The adapter
// validates Configuration and Credential; Query cannot select a compute mutation.
type ConfigurationDiscoveryInput struct {
	Configuration json.RawMessage             `json:"configuration" swaggertype:"object"`
	Credential    json.RawMessage             `json:"credential" swaggertype:"object"`
	Query         ConfigurationDiscoveryQuery `json:"query"`
}

// ConfigurationOption is one choice for a Discovered field. A Complete option's
// Value is the field's value; any other is a Path element for the next level.
type ConfigurationOption struct {
	Value     string     `json:"value" binding:"required"`
	Label     string     `json:"label" binding:"required"`
	Resources *Resources `json:"resources,omitempty"`
	Complete  bool       `json:"complete" binding:"required"`
}

// ConfigurationOptions is the result of one discovery level.
type ConfigurationOptions struct {
	Options []ConfigurationOption `json:"options" binding:"required"`
}

// ConfigurationAdapter owns all interpretation of provider configuration.
// Decode loads retained ownership without remote discovery or new-build admission.
// Normalize validates a candidate; ResolveChange first applies omitted-field
// inheritance, then normalizes. Equal compares normalized identity, excluding
// discovery metadata and explicit credential-submission intent.
//
// The three setup operations are read-only native calls, separate from compute
// and candidate admission. Each builds its own native client from its input.
type ConfigurationAdapter interface {
	Requirements() ConfigurationRequirements
	DecodeInput(public, credential json.RawMessage) (Configuration, error)
	Encode(Configuration) (ConfigurationRecord, error)
	Decode(ConfigurationRecord) (Configuration, error)
	Normalize(Selection) (Selection, error)
	ResolveChange(next, previous Selection) (Selection, error)
	WithCredential(owner, candidate Configuration) (Configuration, error)
	Equal(a, b Configuration) (bool, error)
	// DiscoverConfiguration lists the native catalog a credential can use,
	// without a saved deployment.
	DiscoverConfiguration(context.Context, ConfigurationDiscoveryInput, ProcessPaths) (ConfigurationOptions, error)
	// DiscoverSelection resolves a candidate's omitted native values before
	// commit. Loading retained ownership never calls it.
	DiscoverSelection(context.Context, DirectConfig) (Selection, error)
	// VerifyCredential verifies access to already owned resources without
	// mutation. Replacing a credential requires it and a shared CallFence.
	VerifyCredential(context.Context, DirectConfig, []Reference) error
}

// ConfigurationError is a fixed safe diagnostic, never SDK text or submitted data.
// Class describes the request outcome; it does not authorize replay.
type ConfigurationError struct {
	Class                ConfigurationErrorClass
	Code, Param, Message string
}
type ConfigurationErrorClass string

const (
	ConfigurationInvalid     ConfigurationErrorClass = "invalid"
	ConfigurationConflict    ConfigurationErrorClass = "conflict"
	ConfigurationUnconfirmed ConfigurationErrorClass = "unconfirmed"
)

func (e *ConfigurationError) Error() string { return e.Message }

var (
	ErrCredentialRejected       = &ConfigurationError{ConfigurationInvalid, "sandbox_credential_invalid", "credential", "The sandbox provider credential was rejected."}
	ErrCredentialOwnership      = &ConfigurationError{ConfigurationConflict, "sandbox_credential_ownership", "credential", "The credential cannot manage the retained deployment. Reset before changing accounts."}
	ErrConfigurationUnconfirmed = &ConfigurationError{ConfigurationUnconfirmed, "sandbox_verification_unconfirmed", "", "Sandbox provider verification could not be confirmed."}
	ErrConfigurationSelection   = &ConfigurationError{ConfigurationInvalid, "sandbox_configuration_invalid", "configuration", "Select a ready immutable provider configuration with matching resources."}
)

// ValidateWorkspaceBinding checks the shared ownership envelope before forwarding.
// Adapter-native receipts are validated only by the selected filesystem resolver.
func ValidateWorkspaceBinding(r Reference, binding *workspacefs.Binding) error {
	if binding == nil {
		return nil
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	if binding.Attachment.Reference.TenantID != r.TenantID || binding.Attachment.Reference.EnvironmentID != r.EnvironmentID {
		return workspacefs.ErrOwnership
	}
	return nil
}
