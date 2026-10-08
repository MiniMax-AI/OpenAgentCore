package mcode

import (
	"fmt"
	"net/url"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// workspaceMCP renders the Session's MCP bindings as ACP servers. The Harness
// runs each stdio binding's alias without arguments.
func workspaceMCP(bindings []agent.MCPBinding) ([]map[string]any, error) {
	var servers []map[string]any
	for _, binding := range bindings {
		if binding.Transport != "http" {
			servers = append(servers, map[string]any{"name": binding.ServerLabel, "command": binding.Stdio.Server.Command, "args": []string{}, "env": []map[string]string{}})
			continue
		}
		server, err := environmentHTTPMCP(binding)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, nil
}

// environmentHTTPMCP renders an HTTP binding, the Session gateway's endpoint
// for the server, as an ACP Session server, which remains in native memory.
// The gateway holds the server's credentials, so the binding carries none.
func environmentHTTPMCP(item agent.MCPBinding) (map[string]any, error) {
	endpoint, err := url.Parse(item.ServerURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Opaque != "" ||
		len(item.HTTPHeaders) != 0 || item.BearerToken != nil {
		return nil, fmt.Errorf("mcode: unsupported environment HTTP MCP declaration")
	}
	return map[string]any{"name": item.ServerLabel, "type": "http", "url": item.ServerURL, "headers": []map[string]string{}}, nil
}
