package store

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

// Invalid capacity is reported before the active reset conflict.
func TestRuntimeEnrollmentCapacityPrecedesResetConflict(t *testing.T) {
	s, w, view, _ := webSpecificationFixture(t, "microsandbox")
	nodes := deploymentService(t, s)
	// Enter reset through its owner transaction so the fixture obeys the
	// admission invariant.
	if err := deploymentExecution(t, w).StartReset(SandboxResetTestContext(t.Context()), view.InstallationID, deployment.ResetRequest{ExpectedGeneration: view.Generation, Clear: "auto"}); err != nil {
		t.Fatal(err)
	}
	if _, err := nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 3}); !errors.Is(err, deployment.ErrResetInProgress) {
		t.Fatal("valid capacity did not reach the reset guard", err)
	}
	if _, err := nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 3, MaxRetained: 2}); !errors.Is(err, deployment.ErrInvalidInput) {
		t.Fatal("invalid capacity reported as a conflict", err)
	}
}
