package cli

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

func registerAgentKinds(registry *agent.Registry, discovery agentCLIDiscovery, environments agent.EnvironmentSupport) {
	for _, discovered := range discovery {
		registry.Register(discovered.declaration, discovered.runtime, environments)
	}
}
