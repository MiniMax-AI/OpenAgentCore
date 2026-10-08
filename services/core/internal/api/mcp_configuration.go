package api

import (
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
)

const mcpHTTPOnly = "MCP currently supports HTTP transport only."

// resolveMCPTool canonicalizes a declaration whose pinned shape was checked at
// decode. Core executes HTTP servers only and keeps its local limits.
func resolveMCPTool(raw json.RawMessage, saved bool) (json.RawMessage, error) {
	var input v1.MCPToolInput
	var transport struct {
		Type          string            `json:"type"`
		ServerURL     string            `json:"server_url"`
		Authorization *string           `json:"authorization"`
		Headers       map[string]string `json:"headers"`
	}
	if err := errors.Join(json.Unmarshal(raw, &input), json.Unmarshal(input.Transport, &transport)); err != nil {
		return nil, &storedDataError{err}
	}
	if strings.TrimSpace(input.ServerLabel) == "" {
		return nil, errors.New("MCP tools require type=mcp and a nonempty server_label.")
	}
	if transport.Type != "http" {
		return nil, errors.New(mcpHTTPOnly)
	}
	// The official service saves an omitted or null origin on an HTTP server as
	// "service" (MV-01). Defaulting it here makes the stored and frozen
	// configuration identical to an explicit declaration.
	origin := "service"
	if input.ConnectionOrigin != nil {
		origin = *input.ConnectionOrigin
	}
	if input.CredentialID != nil && *input.CredentialID == "" {
		return nil, errors.New("MCP credential_id must be null or a nonempty string.")
	}
	if len(input.RequestMetadata) != 0 {
		return nil, errors.New("Nonempty MCP request_metadata is not supported yet.")
	}
	if !execution.ValidMCPServerURL(transport.ServerURL) {
		return nil, errors.New("MCP server_url must be an absolute HTTP(S) URL without credentials, query or fragment.")
	}
	if transport.Authorization != nil {
		return nil, errors.New("Inline MCP authorization is not supported yet.")
	}
	if len(transport.Headers) != 0 {
		return nil, errors.New("Nonempty MCP headers are not supported yet.")
	}
	var allowed *[]string
	if input.AllowedTools != nil {
		for _, name := range input.AllowedTools {
			if name == "" {
				return nil, errors.New("MCP allowed_tools requires nonempty string names.")
			}
		}
		allowed = &input.AllowedTools
	}
	tool := v1.MCPTool{Type: "mcp", ServerLabel: input.ServerLabel,
		Transport:    v1.MCPHTTPTransport{Type: "http", ServerURL: transport.ServerURL},
		AllowedTools: allowed, Required: input.Required != nil && *input.Required, ConnectionOrigin: origin, CredentialID: input.CredentialID, RequestMetadata: map[string]json.RawMessage{}}
	if saved {
		headers := map[string]string{}
		tool.Transport.Headers = &headers
	}
	return json.Marshal(tool)
}
