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
	disk := uint32(24063)
	caller.response.DeploymentValid = true
	caller.response.TemplateBuild = &TemplateBuild{Status: "ready", CPUs: 2, MemoryMiB: 2048, RootDiskMiB: &disk}
	build, err := p.ValidateDeployment(context.Background())
	if err != nil || build.CPUs != 2 || build.MemoryMiB != 2048 || build.RootDiskMiB == nil || *build.RootDiskMiB != disk || build.Status != "ready" {
		t.Fatalf("validated build = %+v %v", build, err)
	}
	q := caller.requests[0]
	if q.Operation != "validate_deployment" || q.Reference != (sandbox.Reference{}) || q.Bootstrap != nil || q.Config.Resources.CPUs != 2 || time.Until(q.Deadline) > 30*time.Second || time.Until(q.Deadline) <= 0 {
		t.Fatalf("invalid validation request %+v", q)
	}
	for _, response := range []Response{
		{Version: ProtocolVersion, TemplateBuild: caller.response.TemplateBuild},
		{Version: ProtocolVersion, DeploymentValid: true},
		{Version: ProtocolVersion, DeploymentValid: true, TemplateBuild: &TemplateBuild{Status: "ready", CPUs: 4, MemoryMiB: 2048}},
	} {
		caller.response = response
		if _, err = p.ValidateDeployment(t.Context()); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
			t.Fatal("missing proof accepted", err)
		}
	}
	config.Resources = &sandbox.Resources{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 8192}
	if _, err = NewWithCaller(config, caller); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("unsupported disk accepted", err)
	}
}
