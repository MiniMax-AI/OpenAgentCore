package execution

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func (p Policy) mcpCredentialBindings(engine string, snapshot Snapshot) (map[string]store.MCPCredentialBinding, error) {
	selected, err := selectedMCPCredentials(snapshot)
	if err != nil {
		return nil, err
	}
	profile, qualified := p.Engines.Lookup(engine)
	if len(selected) > 0 && (!qualified || !profile.MCPBearer) {
		return nil, errors.New("The configured engine currently supports anonymous HTTP MCP only.")
	}
	return selected, nil
}

// Selection, final preclaim and request construction use the same combination
// checks. This function never reads plaintext credentials or native configuration.
func (p Policy) mcpExecutionCredentials(engine string, snapshot Snapshot, servers []proto.MCPHTTPServer, caps device.KindCapabilities) (map[string]store.MCPCredentialBinding, error) {
	fail := func(message string) (map[string]store.MCPCredentialBinding, error) {
		return nil, errors.New(message)
	}
	profile, _ := p.Engines.Lookup(engine)
	if err := profile.ValidateMCPOrigins(snapshot.Environment, snapshot.Daemon != nil, servers); err != nil {
		return nil, err
	}
	if len(servers) > 0 && !caps.MCPHTTPTools {
		return fail("device must advertise mcp_http_tools")
	}
	selected, err := p.mcpCredentialBindings(engine, snapshot)
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		if server.Required && !caps.MCPHTTPRequired {
			return fail("device must advertise mcp_http_required")
		}
	}
	if len(selected) > 0 && !caps.MCPHTTPBearerAuth {
		return fail("device must advertise mcp_http_bearer_auth")
	}
	if len(servers) > 0 && snapshot.Environment.Type == "none" && !caps.EnvironmentNone {
		return fail("device must advertise environment_none")
	}
	return selected, nil
}
