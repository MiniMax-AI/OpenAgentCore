package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

const testPublicURL = "https://core.example"

func newService(t *testing.T, storage *fakeStorage, reader *fakeReader, publicURL string) *Service {
	t.Helper()
	registry := providers.Builtin()
	service, err := NewService(storage, reader, registry, newRules(t, registry, publicURL))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// newRules builds the placement rules as cmd/server does.
func newRules(t *testing.T, registry *providers.Registry, publicURL string) *placement.Rules {
	t.Helper()
	rules, err := placement.NewRules(registry, publicURL)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// operations builds execution operations whose every transaction runs on tx.
func operations(t *testing.T, publicURL string, tx *fakeDeploymentTx) *ExecutionOperations {
	t.Helper()
	storage := &fakeExecutionStorage{t: t}
	if tx != nil {
		storage.withDeployment = func(_ context.Context, apply func(DeploymentTx) error) error { return apply(tx) }
	}
	result, err := NewExecutionOperations(newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, publicURL), storage)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// testSpecification is a valid deployment specification for provider.
func testSpecification(provider string) sandbox.DeploymentSpec {
	s := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
	s.Runtime = &sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}
	if provider == "microsandbox" {
		s.Resources.RootDiskMiB = 8192
		s.Resources.EnvironmentDiskMiB = 8192
	}
	return s
}

// webDeployment is a Web-managed node deployment of provider at generation.
func webDeployment(t *testing.T, installation, provider string, generation uint64) Record {
	t.Helper()
	specification, err := json.Marshal(testSpecification(provider))
	if err != nil {
		t.Fatal(err)
	}
	return Record{InstallationID: installation, WebManaged: true, Provider: provider, Generation: generation, Mode: "nodes", Specification: specification,
		Configuration: sandbox.ConfigurationRecord{Public: json.RawMessage(`{}`), Metadata: json.RawMessage(`{}`)}}
}

func TestNewServiceAndOperationsRejectNilDependencies(t *testing.T) {
	storage, reader, registry := &fakeStorage{t: t}, &fakeReader{t: t}, providers.Builtin()
	rules := newRules(t, registry, testPublicURL)
	for name, build := range map[string]func() (*Service, error){
		"storage":  func() (*Service, error) { return NewService(nil, reader, registry, rules) },
		"reader":   func() (*Service, error) { return NewService(storage, nil, registry, rules) },
		"registry": func() (*Service, error) { return NewService(storage, reader, nil, rules) },
		"rules":    func() (*Service, error) { return NewService(storage, reader, registry, nil) },
	} {
		if service, err := build(); service != nil || err == nil {
			t.Errorf("NewService without %s = %v, %v", name, service, err)
		}
	}
	service := newService(t, storage, reader, testPublicURL)
	if result, err := NewExecutionOperations(nil, &fakeExecutionStorage{t: t}); result != nil || err == nil {
		t.Errorf("NewExecutionOperations without the service = %v, %v", result, err)
	}
	if result, err := NewExecutionOperations(service, nil); result != nil || err == nil {
		t.Errorf("NewExecutionOperations without storage = %v, %v", result, err)
	}
}

// A provider whose guests connect to Core from outside the host cannot use a
// loopback public URL. The check writes nothing: the fakes allow no call.
func TestSetupForSelectionRequiresAReachablePublicURL(t *testing.T) {
	id := uuid.NewString()
	e2bSelection := sandbox.Selection{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "synthetic-key", Template: "runtime:" + uuid.NewString()}}
	for _, publicURL := range []string{"http://127.0.0.1:8091", "http://localhost:8091", "http://[::1]:8091"} {
		if _, err := newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, publicURL).SetupForSelection(id, e2bSelection); !errors.Is(err, placement.ErrPublicURLUnreachable) {
			t.Errorf("E2B accepted the loopback public URL %s: %v", publicURL, err)
		}
	}
	setup, err := newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, testPublicURL).SetupForSelection(id, e2bSelection)
	if err != nil || setup.Provider != "e2b" || setup.Mode != "direct" || setup.InstallationID != id || !setup.UsesCredential {
		t.Fatalf("E2B with a public URL = %+v, %v", setup, err)
	}
	docker := sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")}
	if setup, err := newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, "http://127.0.0.1:8091").SetupForSelection(id, docker); err != nil || setup.Mode != "nodes" || setup.UsesCredential {
		t.Fatalf("Docker with a loopback public URL = %+v, %v", setup, err)
	}
}

// An unregistered provider is rejected before any storage call: the fakes
// allow none.
func TestUnknownProviderIsRejectedBeforeStorage(t *testing.T) {
	id := uuid.NewString()
	selection := sandbox.Selection{Provider: "missing-registration", ExpectedGeneration: 1}
	o := operations(t, "http://127.0.0.1", nil)
	service := newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, "http://127.0.0.1")
	for name, call := range map[string]func() error{
		"SetupForSelection": func() error { _, err := service.SetupForSelection(id, selection); return err },
		"Initialize":        func() error { _, err := o.Initialize(t.Context(), id, selection); return err },
		"Update":            func() error { _, err := o.Update(t.Context(), id, selection); return err },
	} {
		var configuration *ConfigurationError
		if err := call(); !errors.As(err, &configuration) || !errors.Is(err, ErrInvalidInput) || configuration.Message != providers.ErrUnknownProvider.Error() {
			t.Errorf("%s accepted an unregistered provider: %v", name, err)
		}
	}
}

// A provider validation failure reaches callers as a ConfigurationError that
// unwraps only ErrInvalidInput and carries the provider's validation metadata
// separately.
func TestSandboxValidationWrapperPreservesClassification(t *testing.T) {
	service := newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, testPublicURL)
	installation := "00000000-0000-4000-8000-000000000001"
	_, err := service.SetupForSelection(installation, sandbox.Selection{Provider: "docker"})
	var configuration *ConfigurationError
	if !errors.As(err, &configuration) || !errors.Is(err, ErrInvalidInput) || configuration.Validation == nil || configuration.Validation.Param != "resources.cpus" {
		t.Fatalf("missing validation metadata: %#v", err)
	}
	if errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("wrapper changed sentinel identity")
	}
	_, err = service.SetupForSelection(installation, sandbox.Selection{Provider: "e2b", DeploymentSpec: sandbox.DeploymentSpec{Runtime: &sandbox.RuntimeRelease{}}})
	if !errors.As(err, &configuration) || configuration.Validation == nil || configuration.Validation.Param != "runtime" || err.Error() != "invalid sandbox configuration: E2B Runtime is selected by its immutable template build" {
		t.Fatal("pending E2B validation order changed", err)
	}
}

// A stored provider the registry no longer holds fails every read and change
// that consults it.
func TestRegistryLookupFailuresPropagate(t *testing.T) {
	stored := webDeployment(t, uuid.NewString(), "retired", 1)
	reader := &fakeReader{t: t,
		snapshot:   func(context.Context) (Snapshot, error) { return Snapshot{Record: stored}, nil },
		deployment: func(context.Context) (Record, error) { return stored, nil },
		nodes: func(context.Context) ([]NodeRecord, error) {
			return []NodeRecord{{ID: uuid.NewString(), Provider: "retired", MaxActive: 1, MaxRetained: 1}}, nil
		},
	}
	service := newService(t, &fakeStorage{t: t}, reader, testPublicURL)
	// View decodes the stored configuration first, and reports that failure as a conflict.
	if _, err := service.View(t.Context()); !errors.Is(err, ErrConflict) {
		t.Errorf("View = %v, want a conflict", err)
	}
	if _, err := service.Setup(t.Context()); !errors.Is(err, providers.ErrUnknownProvider) {
		t.Errorf("Setup = %v", err)
	}
	if _, err := service.ListNodes(t.Context()); !errors.Is(err, providers.ErrUnknownProvider) {
		t.Errorf("ListNodes = %v", err)
	}
	tx := &fakeDeploymentTx{t: t,
		loadDeployment:       func() (Record, error) { return Record{}, nil },
		countResources:       func() (Resources, error) { return Resources{}, nil },
		setProcessDeployment: func(string, string, bool) error { return nil },
	}
	process := &ProcessDeployment{ProviderKind: "retired", InstallationID: uuid.NewString(), BackendFingerprint: strings.Repeat("b", 64)}
	if err := operations(t, testPublicURL, tx).ConfigureProcess(t.Context(), process); !errors.Is(err, providers.ErrUnknownProvider) {
		t.Errorf("ConfigureProcess = %v", err)
	}
}

// Setup carries the mode and operations the provider declares. A stored
// configuration the provider no longer decodes is a conflict the administrator
// resolves by saving again.
func TestSetupReportsDeclarationsAndStoredConfigurationConflicts(t *testing.T) {
	stored := webDeployment(t, uuid.NewString(), "docker", 1)
	reader := &fakeReader{t: t, deployment: func(context.Context) (Record, error) { return stored, nil }}
	service := newService(t, &fakeStorage{t: t}, reader, testPublicURL)
	setup, err := service.Setup(t.Context())
	if err != nil || setup.Mode != "nodes" || setup.Operations["Create"].State != providercontract.Supported {
		t.Fatal(setup, err)
	}
	stored.Configuration.Public = json.RawMessage(`{"retired":true}`)
	if _, err := service.Setup(t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal("an undecodable stored configuration was not a conflict", err)
	}
}

// Docker never suspends, so its nodes retain at most their active capacity.
func TestListNodesAppliesTheRetainedLimit(t *testing.T) {
	reader := &fakeReader{t: t, nodes: func(context.Context) ([]NodeRecord, error) {
		return []NodeRecord{{Provider: "docker", MaxActive: 2, MaxRetained: 5}, {Provider: "microsandbox", MaxActive: 2, MaxRetained: 5}}, nil
	}}
	nodes, err := newService(t, &fakeStorage{t: t}, reader, testPublicURL).ListNodes(t.Context())
	if err != nil || len(nodes) != 2 || nodes[0].MaxRetained != 2 || nodes[1].MaxRetained != 5 {
		t.Fatalf("ListNodes = %+v, %v", nodes, err)
	}
}

func TestSetupChangeDecisions(t *testing.T) {
	installation := uuid.NewString()
	stored := webDeployment(t, installation, "docker", 2)
	load := func() *fakeDeploymentTx {
		return &fakeDeploymentTx{t: t, loadDeployment: func() (Record, error) { return stored, nil }}
	}
	docker := sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker"), ExpectedGeneration: 2}

	stale := docker
	stale.ExpectedGeneration = 1
	for name, call := range map[string]func(*ExecutionOperations) error{
		"CheckSetup": func(o *ExecutionOperations) error { return o.CheckSetup(t.Context(), installation, stale) },
		"Initialize": func(o *ExecutionOperations) error {
			_, err := o.Initialize(t.Context(), installation, stale)
			return err
		},
		"Update": func(o *ExecutionOperations) error { _, err := o.Update(t.Context(), installation, stale); return err },
	} {
		var generation *GenerationStaleError
		if err := call(operations(t, testPublicURL, load())); !errors.As(err, &generation) || generation.CurrentGeneration != 2 || !errors.Is(err, ErrConflict) {
			t.Errorf("%s with a stale generation = %v", name, err)
		}
	}

	switched := sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox"), ExpectedGeneration: 2}
	for name, call := range map[string]func(*ExecutionOperations) error{
		"CheckSetup": func(o *ExecutionOperations) error { return o.CheckSetup(t.Context(), installation, switched) },
		"ClassifyChange": func(o *ExecutionOperations) error {
			_, _, err := o.ClassifyChange(t.Context(), installation, switched)
			return err
		},
		"Update": func(o *ExecutionOperations) error {
			_, err := o.Update(t.Context(), installation, switched)
			return err
		},
	} {
		var reset *ResetRequiredError
		if err := call(operations(t, testPublicURL, load())); !errors.As(err, &reset) || reset.CurrentProvider != "docker" || reset.RequestedProvider != "microsandbox" || !errors.Is(err, ErrConflict) {
			t.Errorf("%s switching provider without a reset = %v", name, err)
		}
	}

	resolved, unchanged, err := operations(t, testPublicURL, load()).ClassifyChange(t.Context(), installation, docker)
	if err != nil || !unchanged || resolved.Provider != "docker" || resolved.ExpectedGeneration != 2 {
		t.Fatalf("ClassifyChange of the stored selection = %+v, %v, %v", resolved, unchanged, err)
	}
	changed := docker
	changed.Resources.CPUs = 4
	if _, unchanged, err := operations(t, testPublicURL, load()).ClassifyChange(t.Context(), installation, changed); err != nil || unchanged {
		t.Fatalf("ClassifyChange of a new specification = %v, %v", unchanged, err)
	}
}

// Enrollment checks the token before the deployment, so a bad token is a
// credential error whatever the deployment's state.
func TestEnrollChecksTheTokenBeforeTheDeployment(t *testing.T) {
	installation, token := uuid.NewString(), "enrollment-token"
	current := webDeployment(t, installation, "docker", 1)
	valid := Enrollment{NodeID: uuid.NewString(), Name: "node", Credential: strings.Repeat("n", 64), Provider: "docker", BackendFingerprint: strings.Repeat("b", 64),
		DeploymentGeneration: 1, SpecificationDigest: testSpecification("docker").Digest("docker"), CoreURL: testPublicURL}
	receipt := EnrollmentRecord{ID: uuid.NewString(), InstallationID: installation, ExpiresAt: time.Now().Add(10 * time.Minute), MaxActive: 1, MaxRetained: 1}
	resetting := current
	resetting.Reset = &ResetState{Clear: "sandboxes", RequestedAt: time.Now()}
	paused := current
	paused.AdmissionPaused = true
	unselected := current
	unselected.Provider = ""
	consumed, expired, foreign := receipt, receipt, receipt
	consumed.Consumed = true
	expired.ExpiresAt = time.Now().Add(-time.Second)
	foreign.InstallationID = uuid.NewString()
	elsewhere := valid
	elsewhere.CoreURL = "https://other.example"
	stale := valid
	stale.DeploymentGeneration = 2

	for _, c := range []struct {
		name       string
		deployment Record
		receipt    EnrollmentRecord
		loadErr    error
		input      Enrollment
		want       error
	}{
		{"unknown token while resetting", resetting, EnrollmentRecord{}, ErrNotFound, valid, ErrNodeCredential},
		{"consumed token before setup", Record{}, consumed, nil, valid, ErrNodeCredential},
		{"expired token while paused", paused, expired, nil, valid, ErrNodeCredential},
		{"token of another installation while resetting", resetting, foreign, nil, valid, ErrNodeCredential},
		{"token store failure", current, EnrollmentRecord{}, errors.New("database down"), valid, placement.ErrNodeUnavailable},
		{"no provider selected", unselected, receipt, nil, valid, placement.ErrNodeUnavailable},
		{"reset in progress", resetting, receipt, nil, valid, ErrResetInProgress},
		{"admission paused", paused, receipt, nil, valid, ErrInvalidInput},
		{"stale generation", current, receipt, nil, stale, ErrSpecificationMismatch},
		// The token stays unused: the fake allows no insert or consumption.
		{"another Core address", current, receipt, nil, elsewhere, ErrNodeAddressMismatch},
	} {
		tx := &fakeNodeTx{t: t,
			loadDeployment: func() (Record, error) { return c.deployment, nil },
			loadEnrollment: func(digest string) (EnrollmentRecord, error) {
				if digest != tokenDigest(token) {
					t.Fatalf("%s: enrollment loaded by %q", c.name, digest)
				}
				return c.receipt, c.loadErr
			},
		}
		storage := &fakeStorage{t: t, withNodes: func(_ context.Context, apply func(NodeTx) error) error { return apply(tx) }}
		if _, err := newService(t, storage, &fakeReader{t: t}, testPublicURL).Enroll(t.Context(), token, c.input); !errors.Is(err, c.want) {
			t.Errorf("%s: Enroll = %v, want %v", c.name, err, c.want)
		}
	}

	// Malformed input is rejected before storage.
	service := newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, testPublicURL)
	for _, mutate := range []func(*Enrollment){
		func(e *Enrollment) { e.NodeID = "node" },
		func(e *Enrollment) { e.Credential = "short" },
		func(e *Enrollment) { e.BackendFingerprint = "fingerprint" },
		func(e *Enrollment) { e.Name = "" },
	} {
		input := valid
		mutate(&input)
		if _, err := service.Enroll(t.Context(), token, input); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Enroll(%+v) = %v", input, err)
		}
	}
}

// Node authentication checks the credential before it reads the deployment.
func TestAuthenticateNodeChecksTheCredentialFirst(t *testing.T) {
	nodeID, credential := uuid.NewString(), strings.Repeat("n", 64)
	stored := StoredNode{ID: nodeID, InstallationID: uuid.NewString(), CredentialDigest: tokenDigest(credential)}
	service := func(reads *fakeNodeReads) *Service {
		reader := &fakeReader{t: t, readNodes: func(_ context.Context, apply func(NodeReads) error) error { return apply(reads) }}
		return newService(t, &fakeStorage{t: t}, reader, testPublicURL)
	}
	if _, err := newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, testPublicURL).AuthenticateNode(t.Context(), "node", credential); !errors.Is(err, ErrNodeCredential) {
		t.Errorf("malformed node ID: %v", err)
	}
	for _, c := range []struct {
		name       string
		node       StoredNode
		loadErr    error
		credential string
		want       error
	}{
		{"unknown node", StoredNode{}, ErrNotFound, credential, ErrNodeCredential},
		{"node store failure", StoredNode{}, errors.New("database down"), credential, placement.ErrNodeUnavailable},
		{"wrong credential", stored, nil, strings.Repeat("x", 64), ErrNodeCredential},
	} {
		reads := &fakeNodeReads{t: t, loadNode: func(id string) (StoredNode, error) {
			if id != nodeID {
				t.Fatalf("%s: loaded node %q", c.name, id)
			}
			return c.node, c.loadErr
		}}
		if _, err := service(reads).AuthenticateNode(t.Context(), nodeID, c.credential); !errors.Is(err, c.want) {
			t.Errorf("%s: AuthenticateNode = %v, want %v", c.name, err, c.want)
		}
	}
	reads := &fakeNodeReads{t: t,
		loadNode:       func(string) (StoredNode, error) { return stored, nil },
		loadDeployment: func() (Record, error) { return webDeployment(t, uuid.NewString(), "docker", 1), nil },
	}
	if _, err := service(reads).AuthenticateNode(t.Context(), nodeID, credential); !errors.Is(err, ErrNodeCredential) {
		t.Errorf("node of another installation: %v", err)
	}
}
