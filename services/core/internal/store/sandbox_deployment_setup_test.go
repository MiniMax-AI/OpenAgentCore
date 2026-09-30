package store

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestSandboxDeploymentSetupPersistsWithoutExecution(t *testing.T) {
	s, pool := newManagedTestStore(t)
	s.SetPublicURL("https://core.example")
	w := executionWriter(t, s)
	id := uuid.NewString()
	if err := deploymentExecution(t, w).Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	before, err := deploymentService(t, s).View(t.Context())
	if err != nil || before.InstallationID != id || before.Provider != "" || before.CoreURL != "https://core.example" {
		t.Fatal(before, err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString())); !errors.Is(err, deployment.ErrNodeUnavailable) {
		t.Fatal("uninitialized hosted admission", err)
	}
	input := sandbox.Selection{DeploymentSpec: SandboxDeploymentTestSpec("microsandbox"), Provider: "microsandbox"}
	selected, err := deploymentExecution(t, w).Initialize(t.Context(), id, input)
	if err != nil || selected.Provider != input.Provider || selected.CoreURL != "https://core.example" || selected.OwnerEpoch != before.OwnerEpoch {
		t.Fatal(selected, err)
	}
	input.ExpectedGeneration = selected.Generation
	replay, err := deploymentExecution(t, w).Initialize(t.Context(), id, input)
	if err != nil || !reflect.DeepEqual(replay, selected) {
		t.Fatal("identical retry changed selection", replay, err)
	}
	for _, changed := range []sandbox.Selection{{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}} {
		if _, err := deploymentExecution(t, w).Initialize(t.Context(), id, changed); !errors.Is(err, deployment.ErrConflict) {
			t.Fatal("changed selection accepted", err)
		}
	}
	setup, err := deploymentService(t, s).Setup(t.Context())
	if err != nil || setup.Suspension == nil || setup.Suspension.IdleSeconds != 300 || setup.Suspension.RetentionSeconds != 86400 || !sha256Hex(setup.BackendFingerprint) {
		t.Fatal(setup, err)
	}
	var sideEffects int
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM sessions)+(SELECT count(*) FROM runtime_nodes)+(SELECT count(*) FROM runtime_allocations)+(SELECT count(*) FROM runtime_placements)").Scan(&sideEffects); err != nil || sideEffects != 0 {
		t.Fatal("setup or rejected admission created execution state", sideEffects, err)
	}
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, w.pool)
	if err := w.lease.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	restarted := executionWriter(t, s)
	if err := deploymentExecution(t, restarted).Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	after, err := deploymentService(t, s).Setup(t.Context())
	if err != nil || !reflect.DeepEqual(after, setup) {
		t.Fatal("restart lost configuration", after, err)
	}
	epoch, err := deploymentStore(s).OwnerEpoch(t.Context())
	if err != nil || epoch != before.OwnerEpoch+1 {
		t.Fatal("restart did not fence node presence", epoch, err)
	}
	if err := deploymentExecution(t, restarted).Claim(t.Context(), uuid.NewString()); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("installation replacement accepted", err)
	}
}

// sha256Hex accepts a lowercase hexadecimal SHA-256 digest.
func sha256Hex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
