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

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
)

var (
	ErrInvalid   = errors.New("invalid sandbox configuration")
	ErrOwnership = errors.New("sandbox ownership mismatch")
	ErrExists    = errors.New("sandbox allocation already exists")
	ErrNotFound  = errors.New("sandbox allocation not found")
	// ErrComputeUnconfirmed requires observation of the retained operation identity;
	// it does not authorize another Create, capture, restore, or cold start.
	ErrComputeUnconfirmed = errors.New("sandbox lifecycle outcome unconfirmed")
)

// Reference must be persisted by the caller before Create. AllocationID is a fresh
// UUID for one attempt, not the Environment ID. Serialize lifecycle operations for
// an allocation; a lost Create response is resolved with GetInfo, never by replay.
type Reference struct{ TenantID, EnvironmentID, AllocationID string }

// Bootstrap is Create's input. The Provider starts one process in the
// sandbox, the Sandbox I/O service, and SandboxIO is its launch input, which
// the Provider delivers as a private file (docs/sandbox-bootstrap.md).
type Bootstrap struct {
	Reference
	SandboxIO sandboxbootstrap.Input
}

// Validate checks a Create input once, before Create: Providers deliver it
// as given. SandboxIO is valid and serves the Reference's allocation, so the
// Reference is valid too.
func (b Bootstrap) Validate() error {
	resource := sandboxbootstrap.Resource{TenantID: b.TenantID, EnvironmentID: b.EnvironmentID, Kind: "allocation", ID: b.AllocationID, Generation: b.SandboxIO.Resource.Generation}
	if b.SandboxIO.Validate() != nil || b.SandboxIO.Resource != resource {
		return ErrInvalid
	}
	return nil
}

// Info describes compute only. Running does not establish a Serving Link
// resource, Environment readiness or a renewable provider lease.
type Info struct {
	Reference
	ProviderID, State string
	// BootstrapComplete is provider evidence that the bootstrap has reached its
	// last mutating step, starting Sandbox I/O. It does not establish Serving.
	BootstrapComplete bool
	// CreateSettled proves that the original create and initialization attempt can
	// no longer mutate resources. An absent observation needs this explicit proof;
	// an ordinary missing resource or empty provider listing is not sufficient.
	CreateSettled bool
}

// SandboxProvider manages one persisted Reference at a time. Every call has a
// bounded context; cancellation ends the caller's wait, not proof of native stop.
// Non-nil errors retain ownership, including partial results. Do not retry Create
// after unknown delivery; observe/reclaim the original Reference.
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

	// The checkpoint lifecycle supplies exact-incarnation operations; Worker and
	// Store remain the lifecycle owner. Its operations share one declaration.
	Initial(context.Context, Reference) (Compute, error)
	NewCompute(context.Context, Reference, uint64, *SnapshotIdentity) (Compute, error)
	GetCompute(context.Context, Reference, Compute) (ComputeState, error)
	Suspend(context.Context, SuspendRequest) (ComputeState, error)
	Resume(context.Context, ResumeRequest) (ComputeState, error)
	KillCompute(context.Context, Reference, Compute) error
	DeleteSnapshot(context.Context, Reference, SnapshotIdentity) error
	// ResumeCompute thaws only the same resident instance after an aborted pause.
	ResumeCompute(context.Context, Reference, Compute) (ComputeState, error)

	// Observe reads one owned Runtime instance after verifying its ownership. It
	// never renews, restarts or stops compute.
	Observe(context.Context, runtimeobs.Target) (runtimeobs.Sample, error)
}

// requiredOperations are supported by every Provider.
var requiredOperations = []string{"Create", "GetInfo", "Renew", "Kill"}

// checkpointOperations are all supported or all unsupported: partial cleanup or
// restore support cannot safely own a compute incarnation.
var checkpointOperations = []string{"Initial", "NewCompute", "GetCompute", "Suspend", "Resume", "KillCompute", "DeleteSnapshot", "ResumeCompute"}

// Compute identifies one incarnation of an allocation. Name is provider-derived.
// ID is empty only until the original create or restore result is observed.
type Compute struct {
	Generation   uint64
	Name         string
	ID           string
	RestoredFrom *SnapshotIdentity
}

// SnapshotIdentity is provider evidence from a verified full snapshot. Core
// persists it unchanged and records consumption separately; it never invents
// paths, checksums, native checkpoint fields, or source identity.
type SnapshotIdentity struct {
	Reference        string
	ID               string
	Digest           string
	CheckpointID     string
	CheckpointRoot   string
	OperationID      string
	SourceGeneration uint64
	SourceName       string
	SourceID         string
}

type ComputeState struct {
	Compute           Compute
	Status            string
	BootstrapComplete bool
	Snapshot          *SnapshotIdentity
	SourceStopped     bool
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
		if operations[name].State != operations["Initial"].State {
			return fmt.Errorf("%w: incomplete checkpoint lifecycle", providercontract.ErrContract)
		}
	}
	return nil
}

func SupportsCheckpoint(p SandboxProvider) bool {
	return providercontract.Require(p, "Initial") == nil
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
	Discovery              providercontract.Support
	SelectionDiscovery     providercontract.Support
	CredentialVerification providercontract.Support
}

// DeploymentPolicy is the deployment declaration. Disk declares independent
// disk limits; Runtime requires a pinned Runtime release, and RuntimeError is
// the fixed reason for rejecting one otherwise. DefaultResources is the size
// setup proposes, or nil when the Provider's configuration selects it.
type DeploymentPolicy struct {
	RuntimeError     string     `json:"-"`
	Disk             bool       `json:"disk"`
	Runtime          bool       `json:"runtime"`
	DefaultResources *Resources `json:"default_resources"`
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
// that adapter alone decodes it, strictly. Resources and the Runtime release
// are read from Specification, never copied into Native.
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
	Probe                              func(context.Context) error
	// Quiescent is nil when no helper can outlive its caller.
	Quiescent func() bool
}

// ConfigurationDiscoveryInput is a transient read-only request. Query is typed
// and validated by the adapter; it cannot select a compute mutation.
type ConfigurationDiscoveryInput struct {
	Configuration json.RawMessage `json:"configuration" swaggertype:"object"`
	Credential    json.RawMessage `json:"credential" swaggertype:"object"`
	Query         json.RawMessage `json:"query" swaggertype:"object"`
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
	DiscoverConfiguration(context.Context, ConfigurationDiscoveryInput, ProcessPaths) (json.RawMessage, error)
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
