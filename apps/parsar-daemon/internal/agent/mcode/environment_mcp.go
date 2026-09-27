package mcode

import (
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func environmentMCP(local *proto.LocalEnvironment) ([]map[string]any, error) {
	if local == nil || len(local.MCP) == 0 {
		return nil, nil
	}
	if local.NetworkAccess != "enabled" {
		return nil, fmt.Errorf("mcode: environment MCP requires enabled network")
	}
	servers := make([]map[string]any, 0, len(local.MCP))
	names := map[string]bool{"oac_workspace": true}
	for _, item := range local.MCP {
		name := item.Server.Name
		if name == "" || strings.TrimSpace(name) != name || names[name] {
			return nil, fmt.Errorf("mcode: ambiguous or unsupported environment MCP identity")
		}
		names[name] = true
		if item.Server.Type == "http" {
			return nil, fmt.Errorf("mcode: environment HTTP MCP is not qualified")
		}
		if item.Server.Type != "stdio" || item.BearerToken != nil {
			return nil, fmt.Errorf("mcode: unsupported environment MCP transport")
		}
		command, args := localworkspace.MCPStdioCommand(item)
		// The fixed launcher resolves the declaration and selected user variables
		// inside the sandbox. Native process variables are never tool input.
		servers = append(servers, map[string]any{"name": name, "command": command, "args": args, "env": []map[string]string{}})
	}
	return servers, nil
}
