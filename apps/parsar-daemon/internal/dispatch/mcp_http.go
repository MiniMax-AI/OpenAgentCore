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
	for _, server := range *req.MCPHTTPServers {
		if err := server.ValidateConnectionOrigin(req); err != nil {
			return err
		}
		if !caps.MCPHTTPTools {
			return errors.New("engine does not support HTTP MCP")
		}
		if server.Required && !caps.MCPHTTPRequired {
			return errors.New("engine does not support required HTTP MCP initialization")
		}
		if server.BearerToken == nil {
			continue
		}
		if !caps.MCPHTTPTools || !caps.MCPHTTPBearerAuth {
			return errors.New("engine does not support authenticated HTTP MCP")
		}
		endpoint, err := url.Parse(server.ServerURL)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" {
			return errors.New("authenticated HTTP MCP requires HTTPS")
		}
	}
	return nil
}
