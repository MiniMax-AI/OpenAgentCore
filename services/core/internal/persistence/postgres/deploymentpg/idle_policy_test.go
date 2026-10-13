package deploymentpg_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestIdlePolicyChangesCurrentAllocationsWithoutRollout(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, before := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
	node := f.enroll(t, before, deployment.Capacity{MaxActive: 1, MaxRetained: 4})
	f.connect(t, node.NodeID)
	owner := runningPressureOwner(t, f, changes, installation, node.NodeID, before.Generation)
	if before.Suspension.IdleSeconds != 300 {
		t.Fatal(before.Suspension)
	}
	until := time.Now().Add(time.Hour)
	if _, err := changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until); !errors.Is(err, deployment.ErrNotIdle) {
		t.Fatal("default idle policy", err)
	}
	for _, invalid := range []int64{0, 29, 86401} {
		if _, err := f.service.UpdateIdleSeconds(admin(t), invalid); !errors.Is(err, deployment.ErrInvalidInput) {
			t.Fatal(invalid, err)
		}
	}
	// A longer saved value is also applied to the existing allocation.
	for _, seconds := range []int64{86400, 30} {
		after, err := f.service.UpdateIdleSeconds(admin(t), seconds)
		if err != nil || after.Suspension.IdleSeconds != seconds || after.Generation != before.Generation || after.SpecificationDigest != before.SpecificationDigest || after.OwnerEpoch != before.OwnerEpoch || after.Suspension.RetentionSeconds != before.Suspension.RetentionSeconds {
			t.Fatal(after, err)
		}
		_, err = changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until)
		if seconds == 86400 && !errors.Is(err, deployment.ErrNotIdle) {
			t.Fatal("increase ignored", err)
		}
		if seconds == 30 && err != nil {
			t.Fatal("decrease ignored", err)
		}
	}
	// Changing policy after the phase commit does not cancel the suspension.
	if _, err := f.service.UpdateIdleSeconds(admin(t), 86400); err != nil {
		t.Fatal(err)
	}
	current, err := f.adapter.EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: owner.TenantID, EnvironmentID: owner.EnvironmentID})
	if err != nil || current.ComputePhase != "quiescing" || current.ComputeRetainedUntil == nil {
		t.Fatal(current, err)
	}
}

func TestIdlePolicyRejectsUnsupportedAndUnconfiguredDeployment(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.UpdateIdleSeconds(admin(t), 60); !errors.Is(err, deployment.ErrNotConfigured) {
		t.Fatal(err)
	}
	changes, _ := f.execution(t)
	f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	if _, err := f.service.UpdateIdleSeconds(admin(t), 60); !errors.Is(err, deployment.ErrInvalidInput) {
		t.Fatal(err)
	}
}
