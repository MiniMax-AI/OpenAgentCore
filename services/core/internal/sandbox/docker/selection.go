package docker

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func Policy() sandbox.DeploymentPolicy {
	return sandbox.DeploymentPolicy{DefaultResources: &sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}
}

// Setup declares how the console connects Docker nodes.
func Setup() sandbox.SetupDeclaration {
	return sandbox.SetupDeclaration{
		Label: "Docker",
		Summary: sandbox.Text{
			EN:   "Runs each sandbox as a container on the host's kernel: CPU and memory limits but no disk quota, for trusted workloads or hosts without KVM.",
			ZHCN: "每个沙箱是一个共用主机内核的容器：能限制 CPU 和内存，但没有磁盘配额，只适合可信的工作负载或没有 KVM 的主机。",
		},
		Cautions: []sandbox.Text{
			{EN: "Containers share the host's kernel, so a container escape reaches the host.", ZHCN: "容器共用主机内核，一旦容器逃逸就能触及主机。"},
			{EN: "The node's service account joins the docker group, which is equivalent to root on that host.", ZHCN: "节点的服务账号会加入 docker 组，这在那台主机上等同于 root 权限。"},
		},
	}
}
