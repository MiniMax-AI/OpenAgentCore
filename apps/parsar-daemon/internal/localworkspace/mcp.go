package localworkspace

import (
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const MCPInitializer = "/usr/local/bin/agents-api-runtime-initialize"

// MCPStdioCommand contains only installed identities. The server's executable,
// arguments and selected user variables are resolved after entering isolation.
func MCPStdioCommand(server proto.EnvironmentMCP) (string, []string) {
	return "/usr/bin/python3", []string{"-I", "-S", MCPInitializer, "stdio", server.PackageRoot, server.Server.Name}
}

func resolveEnvironmentMCP(installed []agentcapabilities.InstalledMCP, values map[string]string) ([]proto.EnvironmentMCP, error) {
	result := make([]proto.EnvironmentMCP, 0, len(installed))
	names := map[string]bool{}
	for _, item := range installed {
		server := item.Server
		if names[server.Name] {
			return nil, errors.New("ambiguous environment MCP server identity")
		}
		names[server.Name] = true
		resolved := proto.EnvironmentMCP{PackageRoot: item.PackageRoot, Server: server}
		variables := append([]string{}, server.EnvVars...)
		if server.BearerTokenEnvVar != "" {
			variables = append(variables, server.BearerTokenEnvVar)
		}
		for _, name := range variables {
			value, exists := values[name]
			if !exists || strings.ContainsRune(value, 0) {
				return nil, errors.New("declared environment MCP variable unavailable")
			}
			if name == server.BearerTokenEnvVar {
				resolved.BearerToken = &value
			}
		}
		result = append(result, resolved)
	}
	return result, nil
}
