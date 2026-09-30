package proto

import "errors"

// MCPHTTPServer declares HTTP tools with an explicit outbound connection origin. Send only to a
// peer advertising mcp_http_tools; a transient BearerToken additionally requires
// mcp_http_bearer_auth. Required initialization requires mcp_http_required.
// Never persist or log this private request as configuration.
type MCPHTTPServer struct {
	ConnectionOrigin string    `json:"connection_origin"`
	ServerLabel      string    `json:"server_label"`
	ServerURL        string    `json:"server_url"`
	AllowedTools     *[]string `json:"allowed_tools"`
	Required         bool      `json:"required,omitempty"`
	BearerToken      *string   `json:"bearer_token,omitempty"`
}

// ValidateConnectionOrigin rejects relocation before dispatch and adapter setup.
func (server MCPHTTPServer) ValidateConnectionOrigin(req PromptRequestPayload) error {
	switch server.ConnectionOrigin {
	case "service":
		if !req.DisableExecutionEnvironment || req.LocalEnvironment != nil {
			return errors.New("service-origin MCP requires a service execution host")
		}
	case "environment":
		if req.DisableExecutionEnvironment || req.LocalEnvironment == nil || req.LocalEnvironment.NetworkAccess != "enabled" {
			return errors.New("environment MCP requires an enabled workspace network")
		}
	default:
		return errors.New("MCP requires an explicit connection origin")
	}
	return nil
}
