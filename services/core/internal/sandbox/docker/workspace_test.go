package docker

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"testing"
)

func TestCreateExplicitlyRejectsExternalWorkspace(t *testing.T) {
	provider := new(Provider)
	_, err := provider.Create(context.Background(), sandbox.Bootstrap{Workspace: &workspacefs.Binding{}})
	if reason, ok := providercontract.UnsupportedReason(err, "Create"); !ok || reason != "external_workspace_unsupported" {
		t.Fatal("external workspace not rejected before native access", err)
	}
}
