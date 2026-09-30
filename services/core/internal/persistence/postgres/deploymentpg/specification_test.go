package deploymentpg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
)

func TestSandboxSpecificationRoundTripAndFileConfigurationCannotOverride(t *testing.T) {
	for _, provider := range []string{"docker", "microsandbox", "e2b"} {
		t.Run(provider, func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			input := sandbox.Selection{Provider: provider, DeploymentSpec: testSpecification(provider)}
			if provider == "e2b" {
				input = setupE2BSelection()
			}
			_, view := f.initialize(t, changes, input)
			setup, err := f.service.Setup(t.Context())
			if err != nil || !reflect.DeepEqual(setup.Specification, input.DeploymentSpec) || view.Specification == nil || !reflect.DeepEqual(*view.Specification, input.DeploymentSpec) || view.SpecificationDigest != input.DeploymentSpec.Digest(provider) {
				t.Fatal("saved deployment lost its resources or Runtime provenance", err)
			}
			preview, err := f.service.SetupForSelection(view.InstallationID, input)
			if err != nil || preview.Mode != setup.Mode || preview.BackendFingerprint != setup.BackendFingerprint || !reflect.DeepEqual(preview.Suspension, setup.Suspension) || !reflect.DeepEqual(preview.Configuration, setup.Configuration) {
				t.Fatal("preview and persisted normalized deployment disagree", err)
			}
			input.ExpectedGeneration = view.Generation
			retry, err := changes.Initialize(t.Context(), view.InstallationID, input)
			if err != nil || !reflect.DeepEqual(retry, view) {
				t.Fatal("identical specification changed the generation", err)
			}
			changed := input
			changed.Resources.CPUs++
			if _, err := changes.Initialize(t.Context(), view.InstallationID, changed); !errors.Is(err, deployment.ErrConflict) {
				t.Fatal("initial setup silently resized a configured deployment", err)
			}
			file := deployment.ProcessDeployment{InstallationID: view.InstallationID, BackendFingerprint: setup.BackendFingerprint, ProviderKind: provider, AdmissionPaused: true}
			for _, candidate := range []*deployment.ProcessDeployment{nil, &file} {
				if err := changes.ConfigureProcess(t.Context(), candidate); !errors.Is(err, deployment.ErrConflict) {
					t.Fatal("file configuration replaced database ownership", err)
				}
			}
			after, err := f.service.View(t.Context())
			if err != nil || !reflect.DeepEqual(after, view) {
				t.Fatal("rejected writes changed the committed specification", err)
			}
		})
	}
}

func TestSandboxSpecificationInitialCredentialRemainsPrivate(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	input := setupE2BSelection()
	key := input.Configuration.(*e2b.DeploymentConfiguration).APIKey
	_, view := f.initialize(t, changes, input)
	raw, err := json.Marshal(view)
	if err != nil || bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte("api_key")) {
		t.Fatal("public deployment serialized a private credential", err)
	}
	var stored []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT provider_credential FROM runtime_deployment").Scan(&stored); err != nil || len(stored) == 0 || bytes.Contains(stored, []byte(key)) {
		t.Fatal("private credential was not encrypted", err)
	}
	// The credential is rejected before the cloud deployment mode is reported.
	if _, err := f.service.NodeConfiguration(t.Context(), "", key, 0); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("cloud key authorized node bootstrap", err)
	}
}

func TestEnrollmentRecordsItsIDAndRefusesAnotherAddress(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	issued, err := f.service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 2, MaxRetained: 2})
	if err != nil || uuid.Validate(issued.ID) != nil || issued.Token == "" {
		t.Fatal(issued.ID, err)
	}
	input := deployment.Enrollment{NodeID: uuid.NewString(), Name: "addressed", Credential: strings.Repeat("a", 64), Provider: "docker",
		BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: "https://other.example"}
	if _, err := f.service.Enroll(t.Context(), issued.Token, input); !errors.Is(err, deployment.ErrNodeAddressMismatch) {
		t.Fatal("enrolled a node that uses another Core address", err)
	}
	input.CoreURL = fixturePublicURL
	if _, err := f.service.Enroll(t.Context(), issued.Token, input); err != nil {
		t.Fatal("the refused enrollment consumed its token", err)
	}
	earlier := strings.Repeat("e", 64)
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO runtime_node_enrollments(token_sha256,installation_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '10 minutes')",
		setupDigest(earlier), view.InstallationID); err != nil {
		t.Fatal(err)
	}
	older := input
	older.NodeID, older.Credential = uuid.NewString(), strings.Repeat("o", 64)
	if _, err := f.service.Enroll(t.Context(), earlier, older); err != nil {
		t.Fatal(err)
	}
	nodes, err := f.service.ListNodes(t.Context())
	if err != nil || len(nodes) != 2 {
		t.Fatal(nodes, err)
	}
	for _, node := range nodes {
		switch node.ID {
		case input.NodeID:
			if node.EnrollmentID == nil || *node.EnrollmentID != issued.ID || node.CoreURL != fixturePublicURL {
				t.Fatal("the node did not record its enrollment", node.EnrollmentID, node.CoreURL)
			}
		case older.NodeID:
			if node.EnrollmentID != nil {
				t.Fatal("a token without an ID reported one", *node.EnrollmentID)
			}
		}
	}
}

func TestDatabaseDoesNotEnumerateProviderRegistrations(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	input := sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")}
	id, view := f.initialize(t, changes, input)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "UPDATE runtime_deployment SET provider_kind='new-adapter' WHERE singleton=true"); err != nil {
		t.Fatal("database enumerated provider implementations", err)
	}
	// Roll back before the deployment operation, which still rejects an
	// unregistered provider even though persistence can represent a new adapter.
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	input.Provider = "new-adapter"
	input.ExpectedGeneration = view.Generation
	if _, err = changes.Initialize(t.Context(), id, input); !errors.Is(err, deployment.ErrInvalidInput) {
		t.Fatal("unknown adapter reached persistence", err)
	}
}
