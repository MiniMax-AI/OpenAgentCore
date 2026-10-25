package microsandbox

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{Workspace: &workspacefs.Requirements{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}, Disk: true,
		DefaultResources: &sandbox.Resources{CPUs: 2, MemoryMiB: 4096, RootDiskMiB: 8192}}
}

// Setup declares how the console connects microsandbox nodes.
func Setup() sandbox.SetupDeclaration {
	return sandbox.SetupDeclaration{
		Label:       "microsandbox",
		Recommended: true,
		Summary: sandbox.Text{
			EN:   "Runs each sandbox as a lightweight virtual machine: stronger isolation and a root disk size limit, with each sandbox's /environment on workspace storage, but the host needs KVM.",
			ZHCN: "每个沙箱是一个轻量虚拟机：隔离更强，能限制根盘大小，每个沙箱的 /environment 位于工作区存储上，但主机需要支持 KVM。",
		},
	}
}
