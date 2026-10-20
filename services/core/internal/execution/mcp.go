package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type executionToolSet struct {
	Functions []proto.FunctionTool
	MCP       []proto.MCPHTTPServer
	Search    bool
	// WebSearch and ProgrammaticToolCalling are set only when the Session declares the tool.
	WebSearch               *proto.WebSearchControl
	ProgrammaticToolCalling *bool
}

func executionTools(raw []json.RawMessage) (executionToolSet, error) {
	functions := make([]json.RawMessage, 0, len(raw))
	var servers []proto.MCPHTTPServer
	search := false
	var webSearch *proto.WebSearchControl
	var programmatic *bool
	names := map[string]bool{}
	for _, value := range raw {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(value, &kind) != nil {
			return executionToolSet{}, errors.New("invalid execution tool")
		}
		if kind.Type == "programmatic_tool_calling" {
			var control struct {
				Enabled *bool `json:"enabled"`
			}
			if json.Unmarshal(value, &control) != nil || control.Enabled == nil {
				return executionToolSet{}, errors.New("invalid programmatic_tool_calling control")
			}
			programmatic = control.Enabled
			continue
		}
		if kind.Type == "web_search" {
			var control struct {
				Mode           string                `json:"mode"`
				ContextSize    string                `json:"context_size"`
				AllowedDomains []string              `json:"allowed_domains"`
				Location       *proto.SearchLocation `json:"location"`
			}
			if json.Unmarshal(value, &control) != nil || control.Mode == "" || control.ContextSize == "" {
				return executionToolSet{}, errors.New("invalid web_search control")
			}
			if location := control.Location; location != nil && location.City == nil && location.Country == nil && location.Region == nil && location.Timezone == nil {
				control.Location = nil
			}
			webSearch = &proto.WebSearchControl{Mode: control.Mode, ContextSize: control.ContextSize, AllowedDomains: control.AllowedDomains, Location: control.Location}
			continue
		}
		if kind.Type == "tool_search" {
			decoder := json.NewDecoder(bytes.NewReader(value))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&kind) != nil || search {
				return executionToolSet{}, errors.New("execution requires one type-only tool_search declaration")
			}
			search = true
			continue
		}
		if kind.Type != "mcp" {
			functions = append(functions, value)
			continue
		}
		var tool v1.MCPTool
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&tool) != nil || strings.TrimSpace(tool.ServerLabel) == "" || names[tool.ServerLabel] || (tool.ConnectionOrigin != "service" && tool.ConnectionOrigin != "environment") || len(tool.RequestMetadata) != 0 {
			return executionToolSet{}, errors.New("unsupported execution MCP configuration")
		}
		if tool.AllowedTools != nil {
			for _, name := range *tool.AllowedTools {
				if name == "" {
					return executionToolSet{}, errors.New("invalid execution MCP tool name")
				}
			}
		}
		server := proto.MCPHTTPServer{ConnectionOrigin: tool.ConnectionOrigin, ServerLabel: tool.ServerLabel, AllowedTools: tool.AllowedTools, Required: tool.Required}
		switch transport := tool.Transport; transport.Type {
		case "stdio":
			if tool.ConnectionOrigin != "environment" || transport.Args == nil || transport.EnvVars == nil || tool.CredentialID != nil {
				return executionToolSet{}, errors.New("unsupported execution MCP configuration")
			}
			server.Stdio = &proto.MCPStdio{Command: transport.Command, Args: *transport.Args, Cwd: transport.Cwd, EnvVars: *transport.EnvVars}
			if !server.Stdio.Valid() {
				return executionToolSet{}, errors.New("unsupported execution MCP configuration")
			}
		case "http":
			u, err := url.Parse(transport.ServerURL)
			if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
				return executionToolSet{}, errors.New("unsupported execution MCP URL")
			}
			server.ServerURL = transport.ServerURL
			if transport.Headers != nil {
				server.Headers = *transport.Headers
			}
		default:
			return executionToolSet{}, errors.New("unsupported execution MCP configuration")
		}
		names[tool.ServerLabel] = true
		servers = append(servers, server)
	}
	resolved, err := functionTools(functions)
	return executionToolSet{Functions: resolved, MCP: servers, Search: search, WebSearch: webSearch, ProgrammaticToolCalling: programmatic}, err
}
