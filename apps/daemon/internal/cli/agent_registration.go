package cli

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

func registerAgentKinds(registry *agent.Registry, discovery agentCLIDiscovery, serverURL string) {
	for _, discovered := range discovery {
		runtime := discovered.runtime
		if runtime.SessionCapabilityContext {
			runtime.Session = withCapabilityDownloads(runtime.Session, serverURL)
		}
		if runtime.Executor != nil && runtime.ExecutorCapabilityContext {
			runtime.Executor = withExecutorCapabilities(runtime.Executor, serverURL)
		}
		registry.Register(discovered.declaration, runtime)
	}
}
