package sandbox

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"testing"
)

func TestExternalStorageProviderRequiresSelectionAndKeepsRootCapacity(t *testing.T) {
	policy := DeploymentPolicy{Disk: true, Workspace: &workspacefs.Requirements{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}}
	declaration := &workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
	spec := DeploymentSpec{Resources: Resources{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 4096}, Workspace: declaration}
	if err := spec.ValidatePolicy("test", policy); err != nil {
		t.Fatal(err)
	}
	if err := (DeploymentSpec{Resources: spec.Resources}).ValidatePolicy("test", policy); !errors.Is(err, ErrWorkspaceRequired) {
		t.Fatal("missing storage accepted", err)
	}
	if err := spec.ValidatePolicy("test", DeploymentPolicy{}); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal("storage accepted by a Provider with its own disk", err)
	}
	quota := spec
	quota.Resources.EnvironmentDiskMiB = 4096
	if err := quota.ValidatePolicy("test", policy); err == nil {
		t.Fatal("unenforced external quota accepted")
	}
	noRoot := spec
	noRoot.Resources.RootDiskMiB = 0
	if err := noRoot.ValidatePolicy("test", policy); err == nil {
		t.Fatal("root quota omitted")
	}
}
