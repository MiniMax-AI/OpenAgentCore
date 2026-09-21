package codex

import (
	"crypto/rand"
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// mergeEnvironmentMCP translates only qualified Runtime declarations. Credential
// references enter the config; values enter the private native env after probes.
func mergeEnvironmentMCP(servers map[string]mcpServerConfig, local *proto.LocalEnvironment) (map[string]mcpServerConfig, []string, error) {
	if local == nil || len(local.MCP) == 0 {
		return servers, nil, nil
	}
	if local.NetworkAccess != "enabled" {
		return nil, nil, errors.New("codex: environment MCP requires enabled network")
	}
	if servers == nil {
		servers = map[string]mcpServerConfig{}
	}
	var env []string
	for _, item := range local.MCP {
		declaration := item.Server
		name := declaration.Name
		if name == "" || strings.TrimSpace(name) != name || name == "codex_apps" {
			return nil, nil, errors.New("codex: unsupported environment MCP identity")
		}
		if _, exists := servers[name]; exists {
			return nil, nil, errors.New("codex: ambiguous environment MCP identity")
		}
		server := mcpServerConfig{Name: name, ApproveTools: true}
		switch declaration.Type {
		case "stdio":
			server.Command, server.Args = localworkspace.MCPStdioCommand(item)
		case "http":
			server.URL = declaration.URL
			if item.BearerToken != nil {
				if !strings.HasPrefix(server.URL, "https://") || !agent.ValidMCPHTTPBearerToken(*item.BearerToken) {
					return nil, nil, errors.New("codex: unsupported environment MCP bearer")
				}
				server.BearerTokenEnvVar = "PARSAR_MCP_BEARER_" + rand.Text()
				env = append(env, server.BearerTokenEnvVar+"="+*item.BearerToken)
			}
			server.EnvHTTPHeaders = map[string]string{}
			for key, value := range declaration.HTTPHeaders {
				// The public value stays literal. This native-only env reference
				// keeps its bytes out of generated configuration and argv.
				reference := "PARSAR_MCP_HEADER_" + rand.Text()
				server.EnvHTTPHeaders[key] = reference
				env = append(env, reference+"="+value)
			}
		default:
			return nil, nil, errors.New("codex: unsupported environment MCP transport")
		}
		servers[name] = server
	}
	return servers, env, nil
}
