package providers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/google/uuid"
)

type regionalConfiguration struct {
	Zone string `json:"zone"`
}

func (regionalConfiguration) HasCredential() bool      { return false }
func (regionalConfiguration) ReplacesCredential() bool { return false }

type regionalCodec struct{}

func (regionalCodec) Requirements() sandbox.ConfigurationRequirements {
	unsupported := providercontract.Support{State: providercontract.Unsupported, Reason: "node_configuration_has_no_catalog"}
	return sandbox.ConfigurationRequirements{Credential: sandbox.NotRequired, Discovery: unsupported, SelectionDiscovery: unsupported, CredentialVerification: unsupported}
}
func (regionalCodec) WithCredential(sandbox.Configuration, sandbox.Configuration) (sandbox.Configuration, error) {
	return nil, &providercontract.UnsupportedError{Operation: "WithCredential", Reason: "credentials_not_required"}
}
func (regionalCodec) DecodeInput(raw, secret json.RawMessage) (sandbox.Configuration, error) {
	var c regionalConfiguration
	if len(secret) > 0 || sandbox.DecodeConfigurationObject(raw, &c, "zone") != nil || c.Zone == "" {
		return nil, sandbox.ErrInvalid
	}
	return c, nil
}
func (a regionalCodec) Decode(r sandbox.ConfigurationRecord) (sandbox.Configuration, error) {
	if len(r.Secret) > 0 || sandbox.DecodeConfigurationObject(r.Metadata, &struct{}{}) != nil {
		return nil, sandbox.ErrInvalid
	}
	return a.DecodeInput(r.Public, nil)
}
func (regionalCodec) Encode(c sandbox.Configuration) (sandbox.ConfigurationRecord, error) {
	v, ok := c.(regionalConfiguration)
	if !ok || v.Zone == "" {
		return sandbox.ConfigurationRecord{}, sandbox.ErrInvalid
	}
	raw, _ := json.Marshal(v)
	return sandbox.ConfigurationRecord{Public: raw, Metadata: json.RawMessage(`{}`)}, nil
}
func (a regionalCodec) Normalize(s sandbox.Selection) (sandbox.Selection, error) {
	_, err := a.Encode(s.Configuration)
	return s, err
}
func (a regionalCodec) ResolveChange(next, previous sandbox.Selection) (sandbox.Selection, error) {
	return a.Normalize(next)
}
func (a regionalCodec) Equal(x, y sandbox.Configuration) (bool, error) {
	_, err := a.Encode(x)
	if err != nil {
		return false, err
	}
	_, err = a.Encode(y)
	return x == y, err
}
func (regionalCodec) DiscoverConfiguration(context.Context, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (json.RawMessage, error) {
	return nil, &providercontract.UnsupportedError{Operation: "DiscoverConfiguration", Reason: "node_configuration_has_no_catalog"}
}
func (regionalCodec) DiscoverSelection(context.Context, sandbox.DirectConfig) (sandbox.Selection, error) {
	return sandbox.Selection{}, &providercontract.UnsupportedError{Operation: "DiscoverSelection", Reason: "node_configuration_has_no_catalog"}
}
func (regionalCodec) VerifyCredential(context.Context, sandbox.DirectConfig, []sandbox.Reference) error {
	return &providercontract.UnsupportedError{Operation: "VerifyCredential", Reason: "node_configuration_has_no_catalog"}
}

// A registered native configuration reaches the ordinary API and Store without
// adding its fields or kind to either Core package.
func TestAdditionalConfigurationProviderUsesCommonAPIAndStore(t *testing.T) {
	// The deployment identity and execution lease are database-wide.
	pool := pgtest.OpenIsolated(t, nil)
	kind := "regional-fixture"
	adapter, err := providers.Builtin().Lookup("docker")
	if err != nil {
		t.Fatal(err)
	}
	adapter.Configuration = regionalCodec{}
	adapter.Policy.Artifacts = map[string]sandbox.ArtifactRule{"regional_image": {Pattern: "sha256:[0-9a-f]{64}", ManifestPath: []string{"regional", "image"}}}
	adapter.ValidateSpecification = func(s sandbox.DeploymentSpec) error { return s.ValidatePolicy(kind, adapter.Policy) }
	registry := providers.FixtureRegistry(t, kind, adapter)
	// The deployment reaches the registered configuration only through the
	// registry it is built with.
	deployments := func() *deployment.Service {
		storage := deploymentpg.New(pgunit.NewPool(pool), pgtest.CredentialKey(t))
		rules, err := placement.NewRules(registry, "https://core.example")
		if err != nil {
			t.Fatal(err)
		}
		service, err := deployment.NewService(storage, storage, registry, rules)
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	service := deployments()
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	changes, err := deployment.NewExecutionOperations(service, deploymentpg.NewExecution(lease, pgtest.CredentialKey(t)))
	if err != nil {
		t.Fatal(err)
	}
	installation := uuid.NewString()
	if err = changes.Claim(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("fixture-admin")})
	if err != nil {
		t.Fatal(err)
	}

	runtime := sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), Artifacts: map[string]string{"regional_image": "sha256:" + strings.Repeat("b", 64)}}
	root := t.TempDir()
	entries := map[string]any{}
	for _, artifact := range adapter.NodeArtifacts {
		entries[artifact.Path] = map[string]any{"filename": runtime.SourceCommit + "-" + artifact.Suffix, "size": 8, "sha256": strings.Repeat("c", 64)}
	}
	manifest, _ := json.Marshal(map[string]any{"source_commit": runtime.SourceCommit, "platform": "linux/amd64", "artifact_base_url": "https://downloads.example/v1", "artifacts": entries, "regional": map[string]string{"image": runtime.Artifacts["regional_image"]}, "images": map[string]string{"runtime": "sha256:" + strings.Repeat("1", 64)}, "image_manifest_digests": map[string]string{"runtime": "sha256:" + strings.Repeat("2", 64)}, "runtime_ref": "oac-runtime@sha256:" + strings.Repeat("3", 64), "microsandbox": map[string]string{"runtime_sha256": strings.Repeat("4", 64), "firmware_sha256": strings.Repeat("5", 64)}})
	files := map[string][]byte{"manifest.json": manifest, "node-install.pyz": []byte("installer"), "runtime/seccomp.json": []byte("{}")}
	sums := ""
	for name, data := range files {
		sums += fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)
	}
	files["SHA256SUMS"] = []byte(sums)
	for name, data := range files {
		path := filepath.Join(root, "node-payload", "releases", runtime.SourceCommit, name)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("fixture publication")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "node-payload", "active.json"), []byte(`{"source_commit":"`+runtime.SourceCommit+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	distribution, err := nativeinstaller.Load(root, runtime.SourceCommit, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer distribution.Close()
	// The flow reaches only the deployment setup; every other dependency
	// panics if called.
	h, err := api.NewHandler(api.Dependencies{
		Engine: "codex", CoreKeys: auth, InstallationBindings: service, Distribution: distribution,
		Projects: struct{ api.Projects }{}, ProjectsReader: struct{ api.ProjectsReader }{},
		ModelProviders: struct{ api.ModelProviders }{}, ModelProvidersReader: struct{ api.ModelProvidersReader }{},
		Vaults: struct{ api.Vaults }{}, VaultsReader: struct{ api.VaultsReader }{},
		Files: struct{ api.Files }{}, FilesReader: struct{ api.FilesReader }{},
		EnvironmentTemplates: struct{ api.EnvironmentTemplates }{}, EnvironmentTemplatesReader: struct{ api.EnvironmentTemplatesReader }{},
		Skills: struct{ api.Skills }{}, SkillsReader: struct{ api.SkillsReader }{},
		Agents: struct{ api.Agents }{}, AgentsReader: struct{ api.AgentsReader }{},
		Sessions:        struct{ api.Sessions }{},
		SessionsReader:  struct{ api.SessionsReader }{},
		SessionCreation: struct{ api.SessionCreation }{},
		SessionEvents:   struct{ api.SessionEvents }{},
		Turns:           struct{ api.Turns }{},
		Items:           struct{ api.Items }{},
		Subagents:       struct{ api.Subagents }{},
		Artifacts:       struct{ api.Artifacts }{},
		ArtifactsReader: struct{ api.ArtifactsReader }{},
		SessionAdmin:    struct{ api.SessionAdmin }{},
		Environments:    struct{ api.Environments }{}, EnvironmentsReader: struct{ api.EnvironmentsReader }{}, Admin: struct{ api.Admin }{}, AdminAudit: struct{ api.AdminAudit }{}, WriteAudit: struct{ api.WriteAudit }{},
		ExecutorConnections: struct{ api.ExecutorConnections }{},
		Metrics:             struct{ api.Metrics }{}, RuntimeObservations: struct{ api.RuntimeObservations }{}, RuntimeHistory: struct{ api.RuntimeHistory }{},
		Execution: api.Execution{
			ExecutorURL:      "wss://core.example/api/v1/agent-daemon/ws",
			SessionAdmission: struct{ api.SessionAdmission }{},
			InputAdmission:   struct{ api.InputAdmission }{},
			SessionArchive:   struct{ api.SessionArchive }{},
			Workspaces:       struct{ api.EnvironmentWorkspaces }{},
			Links:            struct{ http.Handler }{},
			Bootstrap:        http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected machine bootstrap") }), RuntimeConnect: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected Runtime connection") }), Enrollment: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected enrollment") }), Connection: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected connection") }),
		},
		Sandboxes: api.Sandboxes{NodeConnect: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected node connection") }), Deployment: service, NodeAllocations: deploymentpg.New(pgunit.NewPool(pool), pgtest.CredentialKey(t)), DeploymentChanges: leaseSetup{t: t, changes: changes, installation: installation},
			DeploymentReset: leaseSetup{t: t, changes: changes, installation: installation}, ConfigurationDiscovery: struct{ api.ConfigurationDiscovery }{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"provider": kind, "expected_generation": 0, "resources": sandbox.Resources{CPUs: 2, MemoryMiB: 2048}, "configuration": map[string]string{"zone": "west"}})
	request := httptest.NewRequest("POST", "/core/v1/sandbox/deployment", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-admin")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"zone":"west"`) || !strings.Contains(response.Body.String(), `"artifacts":{"regional_image":"`+runtime.Artifacts["regional_image"]+`"}`) {
		t.Fatal(response.Code, response.Body.String())
	}
	saved, err := deployments().Setup(t.Context())
	if err != nil || saved.Configuration.(regionalConfiguration).Zone != "west" {
		t.Fatal("configuration did not roundtrip", err)
	}
	if saved.Specification.Runtime.SourceCommit != runtime.SourceCommit || len(saved.Specification.Runtime.Artifacts) != 1 || saved.Specification.Runtime.Artifacts["regional_image"] != runtime.Artifacts["regional_image"] {
		t.Fatal("adapter artifact did not roundtrip", saved.Specification.Runtime)
	}

	// Missing distribution must not hide a stale generation before preparation.
	missing := sandbox.Selection{Provider: kind, ExpectedGeneration: 0, DeploymentSpec: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}, Configuration: regionalConfiguration{Zone: "west"}}
	var stale *deployment.GenerationStaleError
	if err := changes.CheckSetup(t.Context(), installation, missing); !errors.As(err, &stale) {
		t.Fatal("missing distribution replaced stale generation", err)
	}
	missing.ExpectedGeneration = saved.Generation
	var validation *sandbox.ValidationError
	if err := registry.ValidateSpecification(kind, missing.DeploymentSpec); !errors.As(err, &validation) || validation.Param != "runtime" {
		t.Fatal("missing distribution not typed", err)
	}
	// The Core-selected release is stable across an explicit idempotent setup.
	retry, _ := json.Marshal(map[string]any{"provider": kind, "expected_generation": saved.Generation, "resources": sandbox.Resources{CPUs: 2, MemoryMiB: 2048}, "configuration": map[string]string{"zone": "west"}})
	request = httptest.NewRequest("POST", "/core/v1/sandbox/deployment", bytes.NewReader(retry))
	request.Header.Set("Authorization", "Bearer fixture-admin")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("idempotent Core-selected release", response.Code, response.Body.String())
	}
	var raw []byte
	if err = pool.QueryRow(t.Context(), "SELECT provider_config FROM runtime_deployment").Scan(&raw); err != nil || !strings.Contains(string(raw), `"zone": "west"`) {
		t.Fatal("native fields not persisted", err)
	}
}

// leaseSetup initializes the deployment through the execution lease holder.
// The flow makes no other deployment change.
type leaseSetup struct {
	t            *testing.T
	changes      *deployment.ExecutionOperations
	installation string
}

func (l leaseSetup) InitializeSandboxDeployment(ctx context.Context, in sandbox.Selection) (deployment.View, error) {
	return l.changes.Initialize(ctx, l.installation, in)
}

func (l leaseSetup) UpdateSandboxDeployment(context.Context, sandbox.Selection) (deployment.View, error) {
	l.t.Fatal("unexpected call to UpdateSandboxDeployment")
	return deployment.View{}, nil
}

func (l leaseSetup) StartSandboxReset(context.Context, deployment.ResetRequest) (deployment.View, error) {
	l.t.Fatal("unexpected call to StartSandboxReset")
	return deployment.View{}, nil
}

func (l leaseSetup) CancelSandboxReset(context.Context, uint64) (deployment.View, error) {
	l.t.Fatal("unexpected call to CancelSandboxReset")
	return deployment.View{}, nil
}
