package engine

import (
	"errors"
	"slices"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// ValidateMCPOrigins preserves outbound authority independently of Harness names.
// Workspace connections never stand in for service-network connections.
func (p Profile) ValidateMCPOrigins(environment *v1.Environment, servers []proto.MCPHTTPServer) error {
	for _, server := range servers {
		switch server.ConnectionOrigin {
		case "service":
			if environment == nil || environment.Type != "none" {
				return errors.New("Service-origin MCP requires the service-side environment:none profile.")
			}
		case "environment":
			if environment == nil || (environment.Type != "openai_hosted" && environment.Type != "self_hosted") {
				return errors.New("Environment-origin MCP requires a managed or self-hosted execution Environment.")
			}
		default:
			return errors.New("MCP requires an explicit connection origin.")
		}
		if !slices.Contains(p.MCPOrigins, server.ConnectionOrigin) {
			return errors.New("The configured engine does not support this MCP connection origin.")
		}
	}
	return nil
}
