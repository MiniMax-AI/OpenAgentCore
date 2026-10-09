package claudesdk

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

// Environment servers originate in frozen installed packages. Only native
// transport projection crosses this private bridge, never package configuration.
type environmentMCPServer struct {
	mcpHTTPServer
	Command string `json:"command,omitempty"`
}

// mcpServers renders a view's bindings: each HTTP binding as its gateway
// endpoint, and each stdio binding as its alias, which the Harness runs
// without arguments.
func mcpServers(bindings []agent.MCPBinding) []environmentMCPServer {
	var servers []environmentMCPServer
	for _, binding := range bindings {
		server := environmentMCPServer{mcpHTTPServer: mcpHTTPServer{ServerLabel: binding.ServerLabel}}
		if binding.Stdio != nil {
			server.Command = binding.Stdio.Server.Command
		} else {
			server.ServerURL, server.Required = binding.ServerURL, binding.Required
			if binding.AllowedTools != nil {
				tools := append([]string{}, (*binding.AllowedTools)...)
				server.AllowedTools = &tools
			}
		}
		servers = append(servers, server)
	}
	return servers
}

func (start startRequest) declaredMCP() []mcpHTTPServer {
	var servers []mcpHTTPServer
	if start.MCPHTTPServers != nil {
		servers = append(servers, (*start.MCPHTTPServers)...)
	}
	if start.Workspace != nil {
		for _, server := range start.Workspace.MCP {
			servers = append(servers, server.mcpHTTPServer)
		}
	}
	return servers
}

func (start startRequest) validStdioServers(labels []string) bool {
	if start.Workspace == nil || len(labels) == 0 {
		return false
	}
	seen := make(map[string]bool, len(labels))
	for _, label := range labels {
		if seen[label] {
			return false
		}
		for _, server := range start.Workspace.MCP {
			if server.ServerLabel == label && server.Command != "" {
				seen[label] = true
				break
			}
		}
		if !seen[label] {
			return false
		}
	}
	return true
}
