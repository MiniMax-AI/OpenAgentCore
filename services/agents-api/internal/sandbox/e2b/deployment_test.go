package e2b

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

func TestDeploymentValidationIsBoundedAndNeedsExplicitProof(t *testing.T) {
	p, caller, _ := fixture(t)
	resources := sandbox.Resources{CPUs: 2, MemoryMiB: 2048}
	config := p.config
	config.Resources = &resources
	p, err := NewWithCaller(config, caller)
	if err != nil {
		t.Fatal(err)
	}
	resources.CPUs = 9
	caller.response.DeploymentValid = true
	if err = p.ValidateDeployment(context.Background()); err != nil {
		t.Fatal(err)
	}
	q := caller.requests[0]
	if q.Operation != "validate_deployment" || q.Reference != (sandbox.Reference{}) || q.Bootstrap != nil || q.Config.Resources.CPUs != 2 || time.Until(q.Deadline) > 30*time.Second || time.Until(q.Deadline) <= 0 {
		t.Fatalf("invalid validation request %+v", q)
	}
	caller.response.DeploymentValid = false
	if err = p.ValidateDeployment(t.Context()); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatal("missing proof accepted", err)
	}
	config.Resources = &sandbox.Resources{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 8192}
	if _, err = NewWithCaller(config, caller); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("unsupported disk accepted", err)
	}
}
