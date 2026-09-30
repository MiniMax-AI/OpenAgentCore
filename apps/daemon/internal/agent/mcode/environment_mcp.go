package mcode

import (
	"fmt"
	"net/url"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func runtimeMCP(req proto.PromptRequestPayload) ([]map[string]any, error) {
	bindings, err := agent.ResolveMCPBindings(req)
	if err != nil {
		return nil, err
	}
	var servers []map[string]any
	for _, binding := range bindings {
		if binding.ServerLabel == "oac_workspace" || binding.ConnectionOrigin != "environment" || binding.AllowedTools != nil || binding.Required {
			return nil, fmt.Errorf("mcode: unsupported MCP binding")
		}
		if binding.Transport == "http" {
			server, err := environmentHTTPMCP(binding)
			if err != nil {
				return nil, err
			}
			servers = append(servers, server)
		} else {
			command, args := localworkspace.MCPStdioCommand(*binding.Stdio)
			servers = append(servers, map[string]any{"name": binding.ServerLabel, "command": command, "args": args, "env": []map[string]string{}})
		}
	}
	return servers, nil
}

// ACP session servers remain in native memory; credentials never enter argv or
// persisted native configuration. Custom headers are not qualified because the
// pinned native HTTP client can forward them across redirect origins.
func environmentHTTPMCP(item agent.MCPBinding) (map[string]any, error) {
	endpoint, err := url.Parse(item.ServerURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Opaque != "" || len(item.HTTPHeaders) != 0 {
		return nil, fmt.Errorf("mcode: unsupported environment HTTP MCP declaration")
	}
	headers := []map[string]string{}
	if item.BearerToken != nil {
		if endpoint.Scheme != "https" || !agent.ValidMCPHTTPBearerToken(*item.BearerToken) {
			return nil, fmt.Errorf("mcode: unsupported environment MCP bearer credential")
		}
		headers = append(headers, map[string]string{"name": "Authorization", "value": "Bearer " + *item.BearerToken})
	}
	return map[string]any{"name": item.ServerLabel, "type": "http", "url": item.ServerURL, "headers": headers}, nil
}
