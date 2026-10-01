package store

import (
	"bytes"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func TestSandboxDeploymentMutationViewsIncludeActualResources(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := withPlacement(t, NewWithCredentialCipher(pool, cipher))
	w := executionWriter(t, s)
	changes := deploymentExecution(t, w)
	installation := uuid.NewString()
	if err := changes.Claim(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	selection := e2bSelection()
	if _, err := changes.Initialize(t.Context(), installation, selection); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID}, installation, runtimedevice.HashCredential(uuid.NewString())); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal(err)
	}
	want := deployment.Resources{Allocations: 1, Pending: 1}
	// Replaying setup must report the current resources rather than initial zeros.
	selection.ExpectedGeneration = 1
	replay, err := changes.Initialize(t.Context(), installation, selection)
	if err != nil || replay.Resources != want {
		t.Fatalf("setup replay resources = %+v, error = %v", replay.Resources, err)
	}
	// Reset changes return no view; the view read after each commit reports
	// the current resources.
	for _, maintenance := range []bool{true, false} {
		var err error
		if maintenance {
			err = deploymentExecution(t, w).StartReset(SandboxResetTestContext(t.Context()), installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
		} else {
			err = deploymentExecution(t, w).CancelReset(SandboxResetTestContext(t.Context()), installation, 1)
		}
		if err != nil {
			t.Fatal(err)
		}
		current, err := deploymentService(t, s).View(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if (current.Reset != nil) != maintenance || current.Resources != want {
			t.Fatalf("maintenance %v view = %+v", maintenance, current)
		}
	}
}
