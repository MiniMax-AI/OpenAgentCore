package dispatch

import (
	"errors"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Validate combined placement and authentication before factory selection.
func validateMCPHTTP(req proto.PromptRequestPayload, caps proto.AgentKindCapabilities) error {
	if req.MCPHTTPServers == nil {
		return nil
	}
	if req.LocalEnvironment != nil {
		return errors.New("service-side HTTP MCP is not supported with a local Environment")
	}
	for _, server := range *req.MCPHTTPServers {
		if server.Required && (!caps.MCPHTTPTools || !caps.MCPHTTPRequired || !req.DisableExecutionEnvironment) {
			return errors.New("engine does not support required service-side HTTP MCP initialization")
		}
		if server.BearerToken == nil {
			continue
		}
		if !caps.MCPHTTPTools || !caps.MCPHTTPBearerAuth {
			return errors.New("engine does not support authenticated HTTP MCP")
		}
		if !req.DisableExecutionEnvironment {
			return errors.New("authenticated HTTP MCP requires a supported service-side environment")
		}
		if !caps.EnvironmentNone {
			return errors.New("engine does not support authenticated HTTP MCP with environment:none")
		}
		endpoint, err := url.Parse(server.ServerURL)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" {
			return errors.New("authenticated HTTP MCP requires HTTPS")
		}
	}
	return nil
}
