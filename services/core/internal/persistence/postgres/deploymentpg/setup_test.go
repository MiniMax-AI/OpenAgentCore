package deploymentpg_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
)

// setupE2BSelection is a valid E2B selection with a new template.
func setupE2BSelection() sandbox.Selection {
	return sandbox.Selection{DeploymentSpec: testSpecification("e2b"), Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-private-api-key", Template: "runtime:" + uuid.NewString()}}
}

// setupE2BPublic decodes the public E2B configuration of a view.
func setupE2BPublic(t *testing.T, v deployment.View) *e2b.DeploymentConfiguration {
	t.Helper()
	c, err := (e2b.ConfigurationAdapter{}).Decode(sandbox.ConfigurationRecord{Public: v.Configuration, Metadata: v.Metadata})
	if err != nil {
		t.Fatal(err)
	}
	return c.(*e2b.DeploymentConfiguration)
}

// setupDigest is the hex SHA-256 form in which credentials and enrollment
// tokens are stored.
func setupDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// setupClosedExecution builds the deployment execution operations on an
// execution lease that is already closed. Take it before f.execution: only one
// lease can be held at a time, so it returns after the server has released
// the closed lease.
func setupClosedExecution(t *testing.T, f fixture) *deployment.ExecutionOperations {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, f.pool)
	if err := lease.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	operations, err := deployment.NewExecutionOperations(f.service, deploymentpg.NewExecution(lease, f.cipher))
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

func TestSandboxDeploymentSetupConcurrentSelection(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	id := uuid.NewString()
	if err := changes.Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan deployment.View, 20)
	errorsFound := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provider := "docker"
			if i%2 == 1 {
				provider = "microsandbox"
			}
			value, err := changes.Initialize(t.Context(), id, sandbox.Selection{DeploymentSpec: testSpecification(provider), Provider: provider})
			results <- value
			errorsFound <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errorsFound)
	conflicts := 0
	for err := range errorsFound {
		if errors.Is(err, deployment.ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 19 {
		t.Fatal("both provider selections won", conflicts)
	}
	winner, err := f.service.Setup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for result := range results {
		if result.Provider != "" && result.Provider != winner.Provider {
			t.Fatal("inconsistent selection", result)
		}
	}
}

func TestSandboxDeploymentSetupRejectsFileManagedAndUnleasedWrites(t *testing.T) {
	f := newFixture(t)
	input := sandbox.Selection{DeploymentSpec: testSpecification("docker"), Provider: "docker"}
	process := deployment.ProcessDeployment{InstallationID: uuid.NewString(), BackendFingerprint: strings.Repeat("a", 64), ProviderKind: "docker",
		LocalNodeID: uuid.NewString(), LocalCredentialSHA256: setupDigest("local-node-credential"), LocalMaxActive: 4, LocalMaxRetained: 16}
	// Deployment changes run only on the execution lease; a closed one writes nothing.
	closed := setupClosedExecution(t, f)
	if err := closed.ConfigureProcess(t.Context(), &process); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("unleased process configuration accepted", err)
	}
	if _, err := closed.Initialize(t.Context(), process.InstallationID, input); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("unleased setup accepted", err)
	}
	if view, err := f.service.View(t.Context()); err != nil || view.InstallationID != "" || view.Provider != "" {
		t.Fatal("unleased writes changed the deployment", view, err)
	}
	changes, _ := f.execution(t)
	if err := changes.ConfigureProcess(t.Context(), &process); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.Initialize(t.Context(), process.InstallationID, input); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("file-managed deployment changed", err)
	}
	if err := changes.Claim(t.Context(), process.InstallationID); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("file-managed deployment adopted", err)
	}
}

func TestSandboxSelectionRejectsWhitespaceInE2BCredential(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	id := uuid.NewString()
	if err := changes.Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	for _, separator := range []string{" ", "\t", "\r", "\n", "\x00", " ", " ", "　"} {
		t.Run(fmt.Sprintf("U+%04X", []rune(separator)[0]), func(t *testing.T) {
			selection := setupE2BSelection()
			selection.Configuration.(*e2b.DeploymentConfiguration).APIKey = "prefix" + separator + "suffix"
			if _, err := changes.Initialize(t.Context(), id, selection); !errors.Is(err, deployment.ErrInvalidInput) {
				t.Fatalf("credential containing whitespace or NUL accepted: %v", err)
			}
		})
	}
	if _, err := changes.Initialize(t.Context(), id, setupE2BSelection()); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxE2BEndpointPersistenceAndOnlineSwitch(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	input := setupE2BSelection()
	configured := input.Configuration.(*e2b.DeploymentConfiguration)
	configured.APIURL, configured.Domain = "https://sandbox-test.sandbase.ai", "sandbox-test.sandbase.ai"
	id, view := f.initialize(t, changes, input)
	if view.Configuration == nil || setupE2BPublic(t, view).APIURL != configured.APIURL || setupE2BPublic(t, view).Domain != configured.Domain {
		t.Fatal("custom endpoint was not returned", view)
	}
	setup, err := f.service.Setup(t.Context())
	if err != nil || setup.Configuration == nil || setup.Configuration.(*e2b.DeploymentConfiguration).APIURL != configured.APIURL || setup.Configuration.(*e2b.DeploymentConfiguration).Domain != configured.Domain {
		t.Fatal("custom endpoint was not persisted", setup, err)
	}
	change := setupE2BSelection()
	change.Configuration.(*e2b.DeploymentConfiguration).Template = configured.Template
	change.ExpectedGeneration = view.Generation
	changed, err := changes.Update(admin(t), id, change)
	if err != nil || changed.Generation != view.Generation+1 || changed.Configuration == nil || setupE2BPublic(t, changed).APIURL != "https://api.e2b.app" || setupE2BPublic(t, changed).Domain != "e2b.app" {
		t.Fatal("online endpoint switch failed", changed, err)
	}
}

func TestRuntimeDeploymentRequiresMaintenanceBeforeIdentityChange(t *testing.T) {
	f := newFixture(t)
	old := deployment.ProcessDeployment{InstallationID: uuid.NewString(), BackendFingerprint: strings.Repeat("a", 64)}
	if err := setupClosedExecution(t, f).ConfigureProcess(t.Context(), &old); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("unleased configuration accepted", err)
	}
	changes, _ := f.execution(t)
	configure := func(selected *deployment.ProcessDeployment) {
		t.Helper()
		if err := changes.ConfigureProcess(t.Context(), selected); err != nil {
			t.Fatal(err)
		}
	}
	configure(&old)
	for _, changeID := range []bool{false, true} {
		next := old
		if changeID {
			next.InstallationID = uuid.NewString()
		} else {
			next.BackendFingerprint = strings.Repeat("b", 64)
		}
		next.AdmissionPaused = true
		if err := changes.ConfigureProcess(t.Context(), &next); err == nil || !strings.Contains(err.Error(), "maintenance") {
			t.Fatal("identity changed before prior maintenance", err)
		}
	}
	old.AdmissionPaused = true
	configure(&old)
	next := old
	next.BackendFingerprint = strings.Repeat("b", 64)
	next.AdmissionPaused = false
	if err := changes.ConfigureProcess(t.Context(), &next); err == nil {
		t.Fatal("switch reopened creation in same operation")
	}
	next.AdmissionPaused = true
	configure(&next)
	var id, fingerprint string
	var maintenance bool
	if err := f.pool.QueryRow(t.Context(), "SELECT installation_id::text,backend_fingerprint,admission_paused FROM runtime_deployment").Scan(&id, &fingerprint, &maintenance); err != nil || id != next.InstallationID || fingerprint != next.BackendFingerprint || !maintenance {
		t.Fatal("switch identity not durable", id, fingerprint, maintenance, err)
	}
	configure(nil)
	// Disabling the configured adapter must not forget the old maintenance state.
	next.AdmissionPaused = false
	configure(&next)
	another := deployment.ProcessDeployment{InstallationID: uuid.NewString(), BackendFingerprint: strings.Repeat("a", 64), AdmissionPaused: true}
	if err := changes.ConfigureProcess(t.Context(), &another); err == nil {
		t.Fatal("nil selection erased the maintenance prerequisite")
	}
}
