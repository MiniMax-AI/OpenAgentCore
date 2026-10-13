package deploymentpg_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
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
	operations, err := deployment.NewExecutionOperations(f.service, deploymentpg.NewExecution(lease, f.cipher), engine.Catalog{})
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

func TestSandboxDeploymentSetupRejectsUnleasedWrites(t *testing.T) {
	f := newFixture(t)
	id := uuid.NewString()
	// Deployment changes run only on the execution lease; a closed one writes nothing.
	closed := setupClosedExecution(t, f)
	if err := closed.Claim(t.Context(), id); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("unleased claim accepted", err)
	}
	if _, err := closed.Initialize(t.Context(), id, sandbox.Selection{DeploymentSpec: testSpecification("docker"), Provider: "docker"}); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("unleased setup accepted", err)
	}
	if view, err := f.service.View(t.Context()); err != nil || view.InstallationID != "" || view.Provider != "" {
		t.Fatal("unleased writes changed the deployment", view, err)
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
