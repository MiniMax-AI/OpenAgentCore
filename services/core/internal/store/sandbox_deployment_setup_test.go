package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestSandboxCoreURLValidation(t *testing.T) {
	// deploy/install/test_install.py checks the installer's valid_core_origin against the same cases.
	for _, value := range []string{"https://core.example", "https://core.example:8443", "http://localhost:8091", "http://127.0.0.2:8091", "http://[::1]:8091", "https://[2001:db8::1]"} {
		if err := ValidateSandboxCoreURL(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "http://core.example", "http://core:8091", "http://host.localhost", "https://core.example/", "https://user:secret@core.example", "https://core.example/path", "https://core.example?", "https://core.example?q=x", "https://core.example#x", "https://core.example#", "https://CORE.example", "https://core.example:", "https://core.example:0", "https://core.example:65536", "https://core.example:0080", "https://core.example\\evil"} {
		if err := ValidateSandboxCoreURL(value); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("accepted %q: %v", value, err)
		}
	}
	for _, value := range []string{"https://[not-an-ip]", "https://-core.example", "https://core..example", "https://core_example", "https://core.example.", "https://bücher.example", "https://core.example:0443"} {
		if err := ValidateSandboxCoreURL(value); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("accepted invalid hostname %q: %v", value, err)
		}
	}
}

func TestSandboxDeploymentSetupPersistsWithoutExecution(t *testing.T) {
	s, pool := newManagedTestStore(t)
	s.SetPublicURL("https://core.example")
	lease := executionLease(t, s)
	w := lease.Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || before.InstallationID != id || before.Provider != "" || before.CoreURL != "https://core.example" {
		t.Fatal(before, err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("uninitialized hosted admission", err)
	}
	input := SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("microsandbox"), Provider: "microsandbox"}
	selected, err := w.InitializeSandboxDeployment(t.Context(), id, input)
	if err != nil || selected.Provider != input.Provider || selected.CoreURL != "https://core.example" || selected.OwnerEpoch != before.OwnerEpoch {
		t.Fatal(selected, err)
	}
	input.ExpectedGeneration = selected.Generation
	replay, err := w.InitializeSandboxDeployment(t.Context(), id, input)
	if err != nil || !reflect.DeepEqual(replay, selected) {
		t.Fatal("identical retry changed selection", replay, err)
	}
	for _, changed := range []SandboxDeploymentSetupRequest{{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}} {
		if _, err := w.InitializeSandboxDeployment(t.Context(), id, changed); !errors.Is(err, ErrSandboxDeploymentConflict) {
			t.Fatal("changed selection accepted", err)
		}
	}
	setup, err := s.GetSandboxSetup(t.Context())
	if err != nil || setup.IdleSeconds != 300 || setup.RetentionSeconds != 86400 || !validRuntimeDigest(setup.BackendFingerprint) {
		t.Fatal(setup, err)
	}
	var sideEffects int
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM sessions)+(SELECT count(*) FROM runtime_nodes)+(SELECT count(*) FROM runtime_allocations)+(SELECT count(*) FROM runtime_placements)").Scan(&sideEffects); err != nil || sideEffects != 0 {
		t.Fatal("setup or rejected admission created execution state", sideEffects, err)
	}
	if err := lease.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := executionLease(t, s).Store()
	if err := restarted.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetSandboxSetup(t.Context())
	if err != nil || !reflect.DeepEqual(after, setup) {
		t.Fatal("restart lost configuration", after, err)
	}
	epoch, err := s.RuntimeOwnerEpoch(t.Context())
	if err != nil || epoch != before.OwnerEpoch+1 {
		t.Fatal("restart did not fence node presence", epoch, err)
	}
	if err := restarted.ClaimWebSandboxDeployment(t.Context(), uuid.NewString()); !errors.Is(err, ErrSandboxDeploymentConflict) {
		t.Fatal("installation replacement accepted", err)
	}
}

func TestSandboxDeploymentSetupConcurrentSelection(t *testing.T) {
	s, _ := newManagedTestStore(t)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan RuntimeDeploymentView, 20)
	errorsFound := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provider := "docker"
			if i%2 == 1 {
				provider = "microsandbox"
			}
			value, err := w.InitializeSandboxDeployment(t.Context(), id, SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec(provider), Provider: provider})
			results <- value
			errorsFound <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errorsFound)
	conflicts := 0
	for err := range errorsFound {
		if errors.Is(err, ErrSandboxDeploymentConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 19 {
		t.Fatal("both provider selections won", conflicts)
	}
	winner, err := s.GetSandboxSetup(t.Context())
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
	s, w, selection := managerFixture(t, 4, 16)
	input := SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}
	if _, err := s.InitializeSandboxDeployment(t.Context(), selection.InstallationID, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unleased setup accepted", err)
	}
	if _, err := w.InitializeSandboxDeployment(t.Context(), selection.InstallationID, input); !errors.Is(err, ErrSandboxDeploymentConflict) {
		t.Fatal("file-managed deployment changed", err)
	}
	if err := w.ClaimWebSandboxDeployment(t.Context(), selection.InstallationID); !errors.Is(err, ErrSandboxDeploymentConflict) {
		t.Fatal("file-managed deployment adopted", err)
	}
}

func TestSandboxSelectionRejectsWhitespaceInE2BCredential(t *testing.T) {
	for _, separator := range []string{" ", "\t", "\r", "\n", "\x00", "\u00a0", "\u2003", "\u3000"} {
		t.Run(fmt.Sprintf("U+%04X", []rune(separator)[0]), func(t *testing.T) {
			selection := e2bSelection()
			selection.E2B.APIKey = "prefix" + separator + "suffix"
			if err := validateSandboxSelection(selection); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("credential containing whitespace or NUL accepted: %v", err)
			}
		})
	}
	if err := validateSandboxSelection(e2bSelection()); err != nil {
		t.Fatal(err)
	}
}
