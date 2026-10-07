package integration

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestManagedDeploymentStartupRejectsSwitchBeforeBackendAccess(t *testing.T) {
	s, _ := newManagedTestStore(t)
	key := webDeployment(t, s, "e2b")
	old := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	worker, stop := managedWorker(t, s, key, old)
	tenant, _, environment := managedSession(t, s)
	owner, err := worker.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	replacement := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	start := func(runtimes *execution.RuntimeProvider) error {
		_, err := startNextWorker(t.Context(), s, &execution.Dispatcher{Registry: runtimegateway.NewRegistry(), ManagedRuntimes: runtimes})
		return err
	}
	if err := start(webRuntimes(t, s, uuid.NewString(), replacement, nil)); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("startup switched the claimed installation", err)
	}
	if err := start(nil); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("startup without runtimes abandoned the claimed deployment", err)
	}
	if replacement.creates != 0 || replacement.kills != 0 {
		t.Fatal("rejected startup touched new backend")
	}
	got, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID})
	if err != nil || got.ID != owner.ID || got.ProviderKey != key {
		t.Fatal("rejected startup rewrote resource owner", got, err)
	}
	// Failed startup relinquishes its lease, so the original backend can resume.
	worker, stop = managedWorker(t, s, key, old)
	replay, err := worker.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil || !replay.Replayed || replay.ID != owner.ID {
		t.Fatal("rejected startup interrupted the existing allocation", replay, err)
	}
	stop()
}
