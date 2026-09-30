package store

import (
	"bytes"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func TestSandboxDeploymentMutationViewsIncludeActualResources(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionWriter(t, s)
	installation := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	selection := e2bSelection()
	if _, err := w.InitializeSandboxDeployment(t.Context(), installation, selection); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, installation, runtimedevice.HashCredential(uuid.NewString())); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal(err)
	}
	want := SandboxDeploymentResources{Allocations: 1, Pending: 1}
	// Replaying setup must report the current resources rather than initial zeros.
	selection.ExpectedGeneration = 1
	replay, err := w.InitializeSandboxDeployment(t.Context(), installation, selection)
	if err != nil || replay.Resources != want {
		t.Fatalf("setup replay resources = %+v, error = %v", replay.Resources, err)
	}
	for _, maintenance := range []bool{true, false} {
		var response RuntimeDeploymentView
		var err error
		if maintenance {
			response, err = w.StartSandboxReset(SandboxResetTestContext(t.Context()), installation, SandboxResetRequest{ExpectedGeneration: 1, Clear: "auto"})
		} else {
			response, err = w.CancelSandboxReset(SandboxResetTestContext(t.Context()), installation, 1)
		}
		if err != nil {
			t.Fatal(err)
		}
		current, err := s.GetRuntimeDeployment(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if (response.Reset != nil) != maintenance || response.Resources != want || response.Resources != current.Resources {
			t.Fatalf("maintenance %v response = %+v, current resources = %+v", maintenance, response, current.Resources)
		}
	}
}
