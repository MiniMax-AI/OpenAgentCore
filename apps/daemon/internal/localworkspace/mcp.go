package localworkspace

import (
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

// MCPStdioCommand resolves the common installed manifest in the daemon.
func MCPStdioCommand(server agent.EnvironmentMCP) (string, []string) {
	executable, err := os.Executable()
	if err != nil {
		return "", nil
	}
	return executable, []string{"runtime-mcp-exec", server.InstallationRoot, server.PackageRoot, server.Server.Name}
}

func resolveEnvironmentMCP(installed []agentcapabilities.InstalledMCP, values map[string]string) ([]agent.EnvironmentMCP, error) {
	tokens, err := agentcapabilities.ResolveMCP(installed, values)
	if err != nil {
		return nil, err
	}
	result := make([]agent.EnvironmentMCP, 0, len(installed))
	for i, item := range installed {
		result = append(result, agent.EnvironmentMCP{PackageRoot: item.PackageRoot, Server: item.Server, BearerToken: tokens[i]})
	}
	return result, nil
}
