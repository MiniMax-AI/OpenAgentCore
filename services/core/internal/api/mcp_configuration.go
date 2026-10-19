package api

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

// resolveMCPTool canonicalizes a declaration whose pinned shape was checked at
// decode. Core keeps its local limits.
func resolveMCPTool(raw json.RawMessage, saved bool) (json.RawMessage, error) {
	var input v1.MCPToolInput
	var transport struct {
		Type          string            `json:"type"`
		ServerURL     string            `json:"server_url"`
		Authorization *string           `json:"authorization"`
		Headers       map[string]string `json:"headers"`
		Command       string            `json:"command"`
		Args          []string          `json:"args"`
		Cwd           string            `json:"cwd"`
		EnvVars       []string          `json:"env_vars"`
		Env           map[string]string `json:"env"`
	}
	if err := errors.Join(json.Unmarshal(raw, &input), json.Unmarshal(input.Transport, &transport)); err != nil {
		return nil, &storedDataError{err}
	}
	if strings.TrimSpace(input.ServerLabel) == "" {
		return nil, errors.New("MCP tools require type=mcp and a nonempty server_label.")
	}
	stdio := transport.Type == "stdio"
	// The official service saves an omitted or null origin on an HTTP server as
	// "service" (MV-01). Defaulting it here makes the stored and frozen
	// configuration identical to an explicit declaration. A stdio server runs
	// in the Environment, so "environment" is its default and only origin.
	origin := "service"
	if stdio {
		origin = "environment"
	}
	if input.ConnectionOrigin != nil {
		origin = *input.ConnectionOrigin
	}
	if stdio && origin != "environment" {
		return nil, errors.New("MCP stdio servers run in the Environment; connection_origin must be environment.")
	}
	if input.CredentialID != nil && (*input.CredentialID == "" || stdio) {
		return nil, errors.New("MCP credential_id must be null or a nonempty string, and stdio servers take none.")
	}
	if len(input.RequestMetadata) != 0 {
		return nil, errors.New("Nonempty MCP request_metadata is not supported yet.")
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
	tool := v1.MCPTool{Type: "mcp", ServerLabel: input.ServerLabel, AllowedTools: allowed, Required: input.Required != nil && *input.Required,
		ConnectionOrigin: origin, CredentialID: input.CredentialID, RequestMetadata: map[string]json.RawMessage{}}
	if stdio {
		if len(transport.Env) != 0 {
			return nil, errors.New("Inline MCP env is not supported; declare env_vars and set the values in the Environment.")
		}
		args, envVars := append([]string{}, transport.Args...), append([]string{}, transport.EnvVars...)
		if !(proto.MCPStdio{Command: transport.Command, Args: args, Cwd: transport.Cwd, EnvVars: envVars}).Valid() {
			return nil, errors.New("MCP stdio servers require a nonempty command, an absolute cwd, variable names in env_vars and no NUL characters.")
		}
		tool.Transport = v1.MCPTransport{Type: "stdio", Command: transport.Command, Args: &args, Cwd: transport.Cwd, EnvVars: &envVars}
		return json.Marshal(tool)
	}
	if transport.Type != "http" {
		return nil, errors.New("MCP supports the http and stdio transports only.")
	}
	u, err := url.Parse(transport.ServerURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("MCP server_url must be an absolute HTTP(S) URL without credentials or fragment.")
	}
	if transport.Authorization != nil {
		return nil, errors.New("Inline MCP authorization is not supported; use credential_id.")
	}
	for name := range transport.Headers {
		if strings.EqualFold(name, "Authorization") {
			return nil, errors.New("MCP headers cannot carry Authorization; use credential_id.")
		}
	}
	if !agentplugin.ValidHTTPHeaders(transport.Headers) {
		return nil, errors.New("MCP headers require valid HTTP names and values, with names unique ignoring case.")
	}
	tool.Transport = v1.MCPTransport{Type: "http", ServerURL: transport.ServerURL}
	if len(transport.Headers) != 0 {
		tool.Transport.Headers = &transport.Headers
	} else if saved {
		tool.Transport.Headers = &map[string]string{}
	}
	return json.Marshal(tool)
}
