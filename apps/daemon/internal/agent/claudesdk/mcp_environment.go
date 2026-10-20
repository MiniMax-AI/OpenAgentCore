package claudesdk

import (
	"fmt"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Environment servers originate in frozen installed packages. Only native
// transport projection crosses this private bridge, never package configuration.
type environmentMCPServer struct {
	mcpHTTPServer
	Command string `json:"command,omitempty"`
	// A pointer keeps an empty stdio argument list, which the bridge requires.
	Args *[]string `json:"args,omitempty"`
	// Declared marks a public stdio server: its command is the caller's, not the installed launcher.
	Declared bool `json:"declared,omitempty"`
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
		if process := binding.Process; process != nil {
			// Native starts stdio servers in its workspace, which the declared
			// directory names directly or through the hosted /workspace alias.
			if req.LocalEnvironment == nil || filepath.Clean(process.Cwd) != req.LocalEnvironment.WorkspaceDirectory {
				return nil, nil, fmt.Errorf("claudesdk: MCP stdio cwd must be the workspace directory")
			}
			args := append([]string{}, process.Args...)
			servers = append(servers, environmentMCPServer{mcpHTTPServer: mcpHTTPServer{ServerLabel: binding.ServerLabel, Required: binding.Required}, Command: process.Command, Args: &args, Declared: true})
			continue
		}
		if binding.Stdio != nil {
			command, args := localworkspace.MCPStdioCommand(*binding.Stdio)
			servers = append(servers, environmentMCPServer{mcpHTTPServer: mcpHTTPServer{ServerLabel: binding.ServerLabel}, Command: command, Args: &args})
			continue
		}
		declarations := []proto.MCPHTTPServer{{ServerLabel: binding.ServerLabel, ServerURL: binding.ServerURL, AllowedTools: binding.AllowedTools, Required: binding.Required, BearerToken: binding.BearerToken, Headers: binding.HTTPHeaders}}
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
