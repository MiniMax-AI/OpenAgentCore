package providers

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"testing"
)

func TestWorkspaceReceiptValidatesNormalizationAndGenerationBuild(t *testing.T) {
	registry := Builtin()
	spec := validRegistrationSpec()
	spec.Resources.RootDiskMiB = 4096
	spec.Resources.EnvironmentDiskMiB = 0
	spec.Workspace = &workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
	if err := registry.ValidateSpecification("microsandbox", spec); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Normalize(sandbox.Selection{Provider: "microsandbox", DeploymentSpec: spec}); err != nil {
		t.Fatal(err)
	}
	reached := errors.New("validated generation reached native construction")
	adapter := registry.adapters["microsandbox"]
	adapter.BuildLocal = func(c sandbox.NodeConfig, o sandbox.LocalOptions, b *sandbox.Built) (func(), error) {
		return func() {}, reached
	}
	registry.adapters["microsandbox"] = adapter
	config := sandbox.NodeConfig{Provider: "microsandbox", InstallationID: "11111111-1111-4111-8111-111111111111", Generation: 1, Specification: spec}
	_, _, err := registry.Build(config, sandbox.LocalOptions{Standalone: true, Workspace: unreachableWorkspaceResolver{}})
	if !errors.Is(err, reached) {
		t.Fatal("external retained generation could not build", err)
	}
	if _, _, err = registry.Build(config, sandbox.LocalOptions{Standalone: true}); err == nil || errors.Is(err, reached) {
		t.Fatal("external generation built without resolver", err)
	}
	config.Specification.Workspace = nil
	if _, _, err = registry.Build(config, sandbox.LocalOptions{Standalone: true}); err == nil || errors.Is(err, reached) {
		t.Fatal("owned zero disk bypassed validation", err)
	}
}

type unreachableWorkspaceResolver struct{}

func (unreachableWorkspaceResolver) Resolve(context.Context, workspacefs.Binding) (workspacefs.Directory, error) {
	panic("build must not resolve workspace")
}
