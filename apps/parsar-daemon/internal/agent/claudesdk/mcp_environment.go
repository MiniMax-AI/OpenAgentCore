package claudesdk

import (
	"fmt"

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

func prepareEnvironmentMCP(environment *proto.LocalEnvironment) ([]environmentMCPServer, []string, error) {
	if environment == nil || len(environment.MCP) == 0 {
		return nil, nil, nil
	}
	if environment.NetworkAccess != "enabled" {
		return nil, nil, fmt.Errorf("claudesdk: environment MCP requires enabled network")
	}
	var servers []environmentMCPServer
	var env []string
	labels := map[string]bool{}
	for _, item := range environment.MCP {
		declaration := item.Server
		if !mcpLabel.MatchString(declaration.Name) || declaration.Name == "functions" || labels[declaration.Name] {
			return nil, nil, fmt.Errorf("claudesdk: unsupported environment MCP identity")
		}
		labels[declaration.Name] = true
		switch declaration.Type {
		case "stdio":
			command, args := localworkspace.MCPStdioCommand(item)
			servers = append(servers, environmentMCPServer{mcpHTTPServer: mcpHTTPServer{ServerLabel: declaration.Name}, Command: command, Args: args})
		case "http":
			// The pinned native client expands literal headers again and forwards
			// custom headers across origins. Do not reinterpret their public meaning.
			if len(declaration.HTTPHeaders) != 0 {
				return nil, nil, fmt.Errorf("claudesdk: environment MCP literal HTTP headers are not supported")
			}
			if declaration.BearerTokenEnvVar != "" && item.BearerToken == nil {
				return nil, nil, fmt.Errorf("claudesdk: environment MCP credential unavailable")
			}
			http := []proto.MCPHTTPServer{{ServerLabel: declaration.Name, ServerURL: declaration.URL, BearerToken: item.BearerToken}}
			if err := validateMCPServers(http); err != nil {
				return nil, nil, err
			}
			projected, credentials := prepareMCPHTTP(&http)
			servers = append(servers, environmentMCPServer{mcpHTTPServer: (*projected)[0]})
			env = append(env, credentials...)
		default:
			return nil, nil, fmt.Errorf("claudesdk: unsupported environment MCP transport")
		}
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
