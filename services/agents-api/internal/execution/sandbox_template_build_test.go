package execution

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestOmittedE2BResourcesComeFromValidatedTemplateBuild(t *testing.T) {
	request := store.SandboxDeploymentSetupRequest{Provider: "e2b", CoreURL: "https://core.example", E2B: &store.SandboxE2BConfiguration{APIKey: "key", Template: "runtime:build"}}
	build := &store.SandboxE2BTemplateBuild{Status: "ready", CPUs: 4, MemoryMiB: 4096}
	saved := withTemplateBuild(request, PreparedRuntimeDeployment{E2BTemplateBuild: build})
	if saved.Resources != (sandbox.Resources{CPUs: 4, MemoryMiB: 4096}) || saved.E2B.TemplateBuild == nil || *saved.E2B.TemplateBuild != *build {
		t.Fatalf("validated build was not saved with the selection: %+v", saved)
	}
	if request.Resources != (sandbox.Resources{}) || request.E2B.TemplateBuild != nil {
		t.Fatal("caller's request was changed")
	}
	request.Resources = sandbox.Resources{CPUs: 2, MemoryMiB: 2048}
	if saved = withTemplateBuild(request, PreparedRuntimeDeployment{E2BTemplateBuild: build}); saved.Resources != request.Resources {
		t.Fatalf("explicit resources were replaced: %+v", saved.Resources)
	}
}
