package api

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

const mcpHTTPOnly = "MCP currently supports HTTP transport only."

func resolveMCPTool(raw json.RawMessage, saved bool) (json.RawMessage, error) {
	var input v1.MCPToolInput
	if decodeInputObject(raw, &input, "type", "server_label", "transport", "allowed_tools", "connection_origin", "credential_id", "request_metadata", "required") != nil {
		return nil, errors.New("Invalid MCP tool fields.")
	}
	if input.Type != "mcp" || input.ServerLabel == nil || strings.TrimSpace(*input.ServerLabel) == "" {
		return nil, errors.New("MCP tools require type=mcp and a nonempty server_label.")
	}
	// The official service saves an omitted or null origin on an HTTP server as
	// "service" (MV-01). Defaulting it here makes the stored and frozen
	// configuration identical to an explicit declaration. Other transports are
	// unsupported with any origin.
	if input.ConnectionOrigin == nil {
		if !mcpHTTPTransport(input.Transport) {
			return nil, errors.New(mcpHTTPOnly)
		}
		service := "service"
		input.ConnectionOrigin = &service
	}
	if *input.ConnectionOrigin != "service" {
		return nil, errors.New("MCP currently requires explicit connection_origin=service.")
	}
	if input.CredentialID != nil && *input.CredentialID == "" {
		return nil, errors.New("MCP credential_id must be null or a nonempty string.")
	}
	required, err := optionalBoolean(input.Required, false)
	if err != nil {
		return nil, errors.New("MCP required must be a boolean.")
	}
	if !emptyMCPObject(input.RequestMetadata) {
		return nil, errors.New("Nonempty MCP request_metadata is not supported yet.")
	}
	var transport struct {
		Type      string          `json:"type"`
		ServerURL *string         `json:"server_url"`
		Headers   json.RawMessage `json:"headers"`
	}
	if decodeInputObject(input.Transport, &transport, "type", "server_url", "headers") != nil || transport.Type != "http" || transport.ServerURL == nil {
		return nil, errors.New(mcpHTTPOnly)
	}
	u, err := url.Parse(*transport.ServerURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
		return nil, errors.New("MCP server_url must be an absolute HTTP(S) URL without credentials, query or fragment.")
	}
	if !emptyMCPObject(transport.Headers) {
		return nil, errors.New("Nonempty MCP headers are not supported yet.")
	}
	var allowed *[]string
	if len(input.AllowedTools) != 0 {
		var values *[]*string
		if json.Unmarshal(input.AllowedTools, &values) != nil {
			return nil, errors.New("MCP allowed_tools must be null or an array of tool names.")
		}
		if values != nil {
			names := make([]string, 0, len(*values))
			for _, name := range *values {
				if name == nil || *name == "" {
					return nil, errors.New("MCP allowed_tools requires nonempty string names.")
				}
				names = append(names, *name)
			}
			allowed = &names
		}
	}
	tool := v1.MCPTool{Type: "mcp", ServerLabel: *input.ServerLabel,
		Transport:    v1.MCPHTTPTransport{Type: "http", ServerURL: *transport.ServerURL},
		AllowedTools: allowed, Required: required, ConnectionOrigin: "service", CredentialID: input.CredentialID, RequestMetadata: map[string]json.RawMessage{}}
	if saved {
		headers := map[string]string{}
		tool.Transport.Headers = &headers
	}
	return json.Marshal(tool)
}

// mcpHTTPTransport reports a transport object whose exact "type" member is
// "http". The complete transport is validated afterwards.
func mcpHTTPTransport(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	var kind string
	return json.Unmarshal(raw, &fields) == nil && json.Unmarshal(fields["type"], &kind) == nil && kind == "http"
}

func emptyMCPObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && len(value) == 0
}
