package store_test

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestManagedDeploymentStartupRejectsSwitchBeforeBackendAccess(t *testing.T) {
	s, db := newManagedTestStoreDB(t)
	key := uuid.NewString()
	old := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	worker, stop := managedWorker(t, s, db, key, old)
	tenant, _, environment := managedSession(t, s, db)
	owner, err := worker.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	replacement := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	config := &execution.RuntimeProvider{CoreURL: "http://core.invalid/api/v1", InstallationID: uuid.NewString(), BackendFingerprint: strings.Repeat("b", 64), Provider: replacement, AdmissionPaused: true}
	start := func(config *execution.RuntimeProvider) error {
		_, err := startWorkerErr(t.Context(), db, &execution.Dispatcher{Store: s, Registry: runtimegateway.NewRegistry(), ManagedRuntimes: config})
		return err
	}
	if err := start(config); err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Fatal("startup switched active deployment", err)
	}
	worker, stop = managedWorkerMode(t, s, db, key, old, true)
	replay, err := worker.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil || !replay.Replayed || replay.ID != owner.ID {
		t.Fatal("maintenance interrupted existing allocation", replay, err)
	}
	stop()
	if err := start(config); err == nil || !strings.Contains(err.Error(), "unreleased allocations") {
		t.Fatal("startup abandoned retained allocation", err)
	}
	if err := start(nil); err == nil || !strings.Contains(err.Error(), "unreleased allocations") {
		t.Fatal("omitted configuration abandoned deployment", err)
	}
	if replacement.creates != 0 || replacement.kills != 0 {
		t.Fatal("rejected startup touched new backend")
	}
	got, err := fixtureReader(db).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID})
	if err != nil || got.ID != owner.ID || got.ProviderKey != key {
		t.Fatal("rejected startup rewrote resource owner", got, err)
	}
	// Failed startup relinquishes its lease, so the original backend can resume.
	_, stop = managedWorker(t, s, db, key, old)
	stop()
}
