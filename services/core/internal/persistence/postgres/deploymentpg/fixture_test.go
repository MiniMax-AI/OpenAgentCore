package deploymentpg_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

const fixturePublicURL = "https://core.example"

// The deployment is a database singleton, so every test opens its own
// database.
type fixture struct {
	pool    *pgxpool.Pool
	cipher  *credentialcrypto.Cipher
	adapter *deploymentpg.Store
	service *deployment.Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := pgtest.OpenIsolated(t, nil)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{13}, 32))
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{pool: pool, cipher: cipher}
	f.adapter, f.service = f.withPublicURL(t, fixturePublicURL)
	return f
}

// withPublicURL builds the adapter and service as cmd/server does for an
// installation with this public URL.
func (f fixture) withPublicURL(t *testing.T, publicURL string) (*deploymentpg.Store, *deployment.Service) {
	t.Helper()
	adapter := deploymentpg.New(pgunit.NewPool(f.pool), f.cipher)
	return adapter, newService(t, adapter, publicURL)
}

// newService builds the deployment service on adapter, with the placement
// rules for this public URL, as cmd/server does.
func newService(t *testing.T, adapter *deploymentpg.Store, publicURL string) *deployment.Service {
	t.Helper()
	registry := providers.Builtin()
	rules, err := placement.NewRules(registry, publicURL)
	if err != nil {
		t.Fatal(err)
	}
	service, err := deployment.NewService(adapter, adapter, registry, rules)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// execution acquires the execution lease and builds the deployment execution
// operations on it, as cmd/server does for the Worker. The lease closes when
// the test ends.
func (f fixture) execution(t *testing.T) (*deployment.ExecutionOperations, *pgunit.Lease) {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	operations, err := deployment.NewExecutionOperations(f.service, deploymentpg.NewExecution(lease, f.cipher))
	if err != nil {
		t.Fatal(err)
	}
	return operations, lease
}

// admin carries the administrator provenance that deployment mutations audit.
func admin(t *testing.T) context.Context {
	return adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ActorLabel: "operator", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
}

// testSpecification is a valid deployment specification for provider.
func testSpecification(provider string) sandbox.DeploymentSpec {
	s := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
	if provider == "e2b" {
		return s
	}
	s.Runtime = &sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}
	if provider == "microsandbox" {
		s.Resources.RootDiskMiB = 8192
		s.Resources.EnvironmentDiskMiB = 8192
	}
	return s
}

// initialize claims the deployment for a new installation and selects
// provider, returning the installation ID and the committed view.
func (f fixture) initialize(t *testing.T, changes *deployment.ExecutionOperations, input sandbox.Selection) (string, deployment.View) {
	t.Helper()
	installation := uuid.NewString()
	if err := changes.Claim(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	view, err := changes.Initialize(admin(t), installation, input)
	if err != nil {
		t.Fatal(err)
	}
	return installation, view
}

// enroll issues an enrollment token and enrolls a node on the current
// generation with it.
func (f fixture) enroll(t *testing.T, view deployment.View, capacity deployment.Capacity) deployment.Enrollment {
	t.Helper()
	token, err := f.service.CreateEnrollment(admin(t), capacity)
	if err != nil {
		t.Fatal(err)
	}
	input := deployment.Enrollment{NodeID: uuid.NewString(), Name: "fixture node", Credential: strings.Repeat("n", 64), Provider: view.Provider,
		BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: fixturePublicURL}
	if _, err := f.service.Enroll(t.Context(), token.Token, input); err != nil {
		t.Fatal(err)
	}
	return input
}

// connect connects the node for the current owner epoch and reports it ready.
func (f fixture) connect(t *testing.T, nodeID string) string {
	t.Helper()
	epoch, err := f.adapter.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	connection := uuid.NewString()
	if err := f.service.ConnectNode(t.Context(), nodeID, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Heartbeat(t.Context(), nodeID, connection, epoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
		t.Fatal(err)
	}
	return connection
}
