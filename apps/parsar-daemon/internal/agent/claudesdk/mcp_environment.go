package claudesdk

import (
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Environment servers originate in frozen installed packages. Only native
// transport projection crosses this private bridge, never package configuration.
type environmentMCPServer struct {
	mcpHTTPServer
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func prepareRuntimeMCP(req proto.PromptRequestPayload) ([]environmentMCPServer, []string, error) {
	bindings, err := agent.ResolveMCPBindings(req)
	if err != nil {
		return nil, nil, err
	}
	var servers []environmentMCPServer
	var env []string
	for _, binding := range bindings {
		if !mcpLabel.MatchString(binding.ServerLabel) || binding.ServerLabel == "functions" {
			return nil, nil, fmt.Errorf("claudesdk: unsupported MCP identity")
		}
		if binding.Stdio != nil {
			command, args := localworkspace.MCPStdioCommand(*binding.Stdio)
			servers = append(servers, environmentMCPServer{mcpHTTPServer: mcpHTTPServer{ServerLabel: binding.ServerLabel}, Command: command, Args: args})
			continue
		}
		// Native header interpolation and redirect behavior cannot preserve literal
		// custom-header authority. Reject this unqualified combination explicitly.
		if len(binding.HTTPHeaders) != 0 {
			return nil, nil, fmt.Errorf("claudesdk: literal MCP HTTP headers are not supported")
		}
		declarations := []proto.MCPHTTPServer{{ServerLabel: binding.ServerLabel, ServerURL: binding.ServerURL, AllowedTools: binding.AllowedTools, Required: binding.Required, BearerToken: binding.BearerToken}}
		if err := validateMCPServers(declarations); err != nil {
			return nil, nil, err
		}
		projected, credentials := prepareMCPHTTP(&declarations)
		servers = append(servers, environmentMCPServer{mcpHTTPServer: (*projected)[0]})
		env = append(env, credentials...)
	}
	return servers, env, nil
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
