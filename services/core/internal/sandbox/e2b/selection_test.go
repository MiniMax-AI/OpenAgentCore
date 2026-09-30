package e2b

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestOmittedE2BResourcesComeFromValidatedTemplateBuild(t *testing.T) {
	request := sandbox.Selection{Provider: "e2b", E2B: &sandbox.E2BConfiguration{APIKey: "key", Template: "runtime:build"}}
	build := &sandbox.TemplateBuild{Status: "ready", CPUs: 4, MemoryMiB: 4096}
	saved := WithTemplateBuild(request, build)
	if saved.Resources != (sandbox.Resources{CPUs: 4, MemoryMiB: 4096}) || saved.E2B.TemplateBuild == nil || *saved.E2B.TemplateBuild != *build {
		t.Fatalf("validated build was not saved with the selection: %+v", saved)
	}
	if request.Resources != (sandbox.Resources{}) || request.E2B.TemplateBuild != nil {
		t.Fatal("caller's request was changed")
	}
	request.Resources = sandbox.Resources{CPUs: 2, MemoryMiB: 2048}
	if saved = WithTemplateBuild(request, build); saved.Resources != request.Resources {
		t.Fatalf("explicit resources were replaced: %+v", saved.Resources)
	}
}
