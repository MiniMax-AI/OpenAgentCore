package sandbox

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"testing"
)

func TestWorkspaceQuotaDoesNotWeakenRootCapacity(t *testing.T) {
	policy := DeploymentPolicy{Disk: true, Workspace: &workspacefs.Requirements{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}}
	declaration := workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
	resources := Resources{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 4096}
	if err := resources.ValidateWorkspacePolicy("test", policy, &declaration); err != nil {
		t.Fatal(err)
	}
	if err := resources.ValidatePolicy("test", policy); err == nil {
		t.Fatal("owned disk accepted zero")
	}
	resources.EnvironmentDiskMiB = 4096
	if err := resources.ValidateWorkspacePolicy("test", policy, &declaration); err == nil {
		t.Fatal("unenforced external quota accepted")
	}
	resources.EnvironmentDiskMiB = 0
	resources.RootDiskMiB = 0
	if err := resources.ValidateWorkspacePolicy("test", policy, &declaration); err == nil {
		t.Fatal("root quota omitted")
	}
}
