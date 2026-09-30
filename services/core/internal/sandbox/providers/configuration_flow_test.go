package providers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

type regionalConfiguration struct {
	Zone string `json:"zone"`
}

func (regionalConfiguration) HasCredential() bool      { return false }
func (regionalConfiguration) ReplacesCredential() bool { return false }

type regionalCodec struct{}

func (regionalCodec) Requirements() sandbox.ConfigurationRequirements {
	return sandbox.ConfigurationRequirements{Credential: sandbox.NotRequired, PublicOrigin: sandbox.NotRequired, Discovery: providercontract.Support{State: providercontract.Unsupported, Reason: "node_configuration_has_no_catalog"}}
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

// A registered native configuration reaches the ordinary API and Store without
// adding its fields or kind to either Core package.
func TestAdditionalConfigurationProviderUsesCommonAPIAndStore(t *testing.T) {
	// The deployment identity and execution lease are database-wide.
	pool := pgtest.OpenIsolated(t, nil)
	kind := "regional-fixture"
	adapter, err := providers.Lookup("docker")
	if err != nil {
		t.Fatal(err)
	}
	adapter.Configuration = regionalCodec{}
	providers.RegisterFixture(t, kind, adapter)
	s := store.New(pool)
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	w := store.NewExecution(s, lease)
	installation := uuid.NewString()
	if err = w.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("fixture-admin")})
	if err != nil {
		t.Fatal(err)
	}
	// The flow reaches only the store areas and the deployment setup; every
	// other dependency panics if called.
	h, err := api.NewHandler(api.Dependencies{
		Engine: "codex", CoreKeys: auth, InstallationBindings: s,
		Projects: struct{ api.Projects }{}, ProjectsReader: struct{ api.ProjectsReader }{},
		ModelProviders: struct{ api.ModelProviders }{}, ModelProvidersReader: struct{ api.ModelProvidersReader }{},
		Vaults: struct{ api.Vaults }{}, VaultsReader: struct{ api.VaultsReader }{},
		Files: struct{ api.Files }{}, FilesReader: struct{ api.FilesReader }{},
		EnvironmentTemplates: struct{ api.EnvironmentTemplates }{}, EnvironmentTemplatesReader: struct{ api.EnvironmentTemplatesReader }{},
		Skills: struct{ api.Skills }{}, SkillsReader: struct{ api.SkillsReader }{},
		Agents: struct{ api.Agents }{}, AgentsReader: struct{ api.AgentsReader }{},
		Sessions: s, SessionEvents: s, SessionHistory: s, Subagents: s, Artifacts: s,
		SessionAdmin: s, Environments: s, Admin: s, AdminAudit: struct{ api.AdminAudit }{}, WriteAudit: struct{ api.WriteAudit }{},
		ExecutorConnections: struct{ api.ExecutorConnections }{},
		Metrics:             struct{ api.Metrics }{}, RuntimeObservations: struct{ api.RuntimeObservations }{}, RuntimeHistory: struct{ api.RuntimeHistory }{},
		Execution: &api.Execution{ExecutorURL: "wss://core.example/api/v1/agent-daemon/ws", Admission: s, SessionArchive: s, Workspaces: struct{ api.EnvironmentWorkspaces }{}},
		Sandboxes: &api.Sandboxes{Deployment: s, DeploymentChanges: leaseSetup{t: t, store: w, installation: installation}, ConfigurationDiscovery: struct{ api.ConfigurationDiscovery }{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}
	body, _ := json.Marshal(map[string]any{"provider": kind, "expected_generation": 0, "resources": sandbox.Resources{CPUs: 2, MemoryMiB: 2048}, "runtime": runtime, "configuration": map[string]string{"zone": "west"}})
	request := httptest.NewRequest("POST", "/core/v1/sandbox/deployment", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-admin")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"zone":"west"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	reopened := store.New(pool)
	saved, err := reopened.GetSandboxSetup(t.Context())
	if err != nil || saved.Configuration.(regionalConfiguration).Zone != "west" {
		t.Fatal("configuration did not roundtrip", err)
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
	store        *store.Store
	installation string
}

func (l leaseSetup) InitializeSandboxDeployment(ctx context.Context, in store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
	return l.store.InitializeSandboxDeployment(ctx, l.installation, in)
}

func (l leaseSetup) UpdateSandboxDeployment(context.Context, store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error) {
	l.t.Fatal("unexpected call to UpdateSandboxDeployment")
	return store.RuntimeDeploymentView{}, nil
}

func (l leaseSetup) StartSandboxReset(context.Context, store.SandboxResetRequest) (store.RuntimeDeploymentView, error) {
	l.t.Fatal("unexpected call to StartSandboxReset")
	return store.RuntimeDeploymentView{}, nil
}

func (l leaseSetup) CancelSandboxReset(context.Context, uint64) (store.RuntimeDeploymentView, error) {
	l.t.Fatal("unexpected call to CancelSandboxReset")
	return store.RuntimeDeploymentView{}, nil
}
