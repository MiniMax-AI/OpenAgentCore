package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type executionToolSet struct {
	Functions           []proto.FunctionTool
	MCP                 []proto.MCPHTTPServer
	Search              bool
	DisableProgrammatic bool
}

func executionTools(raw []json.RawMessage) (executionToolSet, error) {
	functions := make([]json.RawMessage, 0, len(raw))
	var servers []proto.MCPHTTPServer
	search := false
	disableProgrammatic := false
	controls := map[string]bool{}
	names := map[string]bool{}
	for _, value := range raw {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(value, &kind) != nil {
			return executionToolSet{}, errors.New("invalid execution tool")
		}
		if kind.Type == "programmatic_tool_calling" || kind.Type == "web_search" {
			if controls[kind.Type] {
				return executionToolSet{}, errors.New("execution requires distinct tool controls")
			}
			controls[kind.Type] = true
			if kind.Type == "programmatic_tool_calling" {
				var control struct {
					Type    string `json:"type"`
					Enabled *bool  `json:"enabled"`
				}
				decoder := json.NewDecoder(bytes.NewReader(value))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&control) != nil || control.Enabled == nil || *control.Enabled {
					return executionToolSet{}, errors.New("programmatic tool calling is not qualified for execution")
				}
				disableProgrammatic = true
			} else {
				var control struct {
					Mode string `json:"mode"`
				}
				if json.Unmarshal(value, &control) != nil || control.Mode != "disabled" {
					return executionToolSet{}, errors.New("only disabled web search is qualified for execution")
				}
			}
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
		if decoder.Decode(&tool) != nil || strings.TrimSpace(tool.ServerLabel) == "" || names[tool.ServerLabel] || tool.ConnectionOrigin != "service" || len(tool.RequestMetadata) != 0 || tool.Transport.Type != "http" || tool.Transport.Headers != nil {
			return executionToolSet{}, errors.New("unsupported execution MCP configuration")
		}
		u, err := url.Parse(tool.Transport.ServerURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
			return executionToolSet{}, errors.New("unsupported execution MCP URL")
		}
		if tool.AllowedTools != nil {
			for _, name := range *tool.AllowedTools {
				if name == "" {
					return executionToolSet{}, errors.New("invalid execution MCP tool name")
				}
			}
		}
		names[tool.ServerLabel] = true
		servers = append(servers, proto.MCPHTTPServer{ServerLabel: tool.ServerLabel,
			ServerURL: tool.Transport.ServerURL, AllowedTools: tool.AllowedTools, Required: tool.Required})
	}
	resolved, err := functionTools(functions)
	return executionToolSet{Functions: resolved, MCP: servers, Search: search, DisableProgrammatic: disableProgrammatic}, err
}
