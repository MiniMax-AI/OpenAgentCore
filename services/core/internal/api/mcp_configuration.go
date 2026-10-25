package api

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

// resolveMCPTool canonicalizes a declaration whose pinned shape and server_label
// were checked at decode. Core keeps its local limits; a rejected transport
// member is named under at, the declaration's path.
func resolveMCPTool(raw json.RawMessage, at string, saved bool) (json.RawMessage, error) {
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
	reject := func(member, message string) error {
		return &fieldError{param: at + "." + member, message: message}
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
		return nil, errors.New("No engine sends MCP request_metadata.")
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
			return nil, reject("transport.env", "Inline MCP env is not supported; declare env_vars and set the values in the Environment.")
		}
		args, envVars := append([]string{}, transport.Args...), append([]string{}, transport.EnvVars...)
		// Valid is the one definition of a startable declaration; each probe
		// varies one member over a valid remainder to name the member at fault,
		// and the whole declaration is checked last.
		declaration := proto.MCPStdio{Command: transport.Command, Args: args, Cwd: transport.Cwd, EnvVars: envVars}
		for _, probe := range []struct {
			member string
			stdio  proto.MCPStdio
		}{
			{"transport.command", proto.MCPStdio{Command: transport.Command, Cwd: "/"}},
			{"transport.args", proto.MCPStdio{Command: "x", Args: args, Cwd: "/"}},
			{"transport.cwd", proto.MCPStdio{Command: "x", Cwd: transport.Cwd}},
			{"transport.env_vars", proto.MCPStdio{Command: "x", Cwd: "/", EnvVars: envVars}},
			{"transport", declaration},
		} {
			if !probe.stdio.Valid() {
				return nil, reject(probe.member, "MCP stdio servers require a nonempty command, an absolute cwd, variable names in env_vars and no NUL characters.")
			}
		}
		tool.Transport = v1.MCPTransport{Type: "stdio", Command: transport.Command, Args: &args, Cwd: transport.Cwd, EnvVars: &envVars}
		return json.Marshal(tool)
	}
	if transport.Type != "http" {
		return nil, reject("transport.type", "MCP supports the http and stdio transports only.")
	}
	u, err := url.Parse(transport.ServerURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, reject("transport.server_url", "MCP server_url must be an absolute HTTP(S) URL without credentials or fragment.")
	}
	if transport.Authorization != nil {
		return nil, reject("transport.authorization", "Inline MCP authorization is not supported; use credential_id.")
	}
	names, seen := make([]string, 0, len(transport.Headers)), map[string]bool{}
	for name := range transport.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.EqualFold(name, "Authorization") {
			return nil, reject("transport.headers."+name, "MCP headers cannot carry Authorization; use credential_id.")
		}
		lower := strings.ToLower(name)
		if seen[lower] || !agentplugin.ValidHTTPHeaders(map[string]string{name: transport.Headers[name]}) {
			return nil, reject("transport.headers."+name, "MCP headers require valid HTTP names and values, with names unique ignoring case.")
		}
		seen[lower] = true
	}
	tool.Transport = v1.MCPTransport{Type: "http", ServerURL: transport.ServerURL}
	if len(transport.Headers) != 0 {
		tool.Transport.Headers = &transport.Headers
	} else if saved {
		tool.Transport.Headers = &map[string]string{}
	}
	return json.Marshal(tool)
}
