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
	servers := []map[string]any{}
	for _, binding := range bindings {
		if binding.Transport == "http" {
			server, err := httpMCP(binding)
			if err != nil {
				return nil, err
			}
			servers = append(servers, server)
		} else if binding.Stdio != nil {
			command, args := localworkspace.MCPStdioCommand(*binding.Stdio)
			servers = append(servers, map[string]any{"name": binding.ServerLabel, "command": command, "args": args, "env": []map[string]string{}})
		} else {
			return nil, fmt.Errorf("mcode: public MCP stdio servers are unsupported")
		}
	}
	return servers, nil
}

// ACP session servers remain in native memory; credentials never enter argv or
// persisted native configuration.
func httpMCP(item agent.MCPBinding) (map[string]any, error) {
	endpoint, err := url.Parse(item.ServerURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return nil, fmt.Errorf("mcode: unsupported HTTP MCP declaration")
	}
	headers := []map[string]string{}
	for name, value := range item.HTTPHeaders {
		headers = append(headers, map[string]string{"name": name, "value": value})
	}
	if item.BearerToken != nil {
		if endpoint.Scheme != "https" || !agent.ValidMCPHTTPBearerToken(*item.BearerToken) {
			return nil, fmt.Errorf("mcode: unsupported MCP bearer credential")
		}
		headers = append(headers, map[string]string{"name": "Authorization", "value": "Bearer " + *item.BearerToken})
	}
	return map[string]any{"name": item.ServerLabel, "type": "http", "url": item.ServerURL, "headers": headers}, nil
}
