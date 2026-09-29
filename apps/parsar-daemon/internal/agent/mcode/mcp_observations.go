package mcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type mcpToolIdentity struct{ server, tool string }

// MiniMax 0.4.12 atomically writes these assignments before exposing tools.
// Read its exact mapping instead of reversing lossy native name normalization.
func (s *Session) environmentMCPIdentity(name string) (*mcpToolIdentity, error) {
	if s.req.LocalEnvironment == nil || len(s.req.LocalEnvironment.MCP) == 0 ||
		!strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "mcp__oac_workspace__") {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(s.opts.DataDir, "mcp-runtime-names.json"))
	if err != nil {
		return nil, fmt.Errorf("mcode: native MCP identity registry unavailable")
	}
	var registry struct {
		Version int `json:"version"`
		Servers []struct {
			Key, Raw, Segment string
			Tools             []struct{ Raw, Segment string }
		} `json:"servers"`
	}
	if json.Unmarshal(raw, &registry) != nil || registry.Version != 1 {
		return nil, fmt.Errorf("mcode: invalid native MCP identity registry")
	}
	var found *mcpToolIdentity
	for _, server := range registry.Servers {
		for _, tool := range server.Tools {
			if "mcp__"+server.Segment+"__"+tool.Segment != name {
				continue
			}
			var key []string
			declared := 0
			for _, item := range s.req.LocalEnvironment.MCP {
				if item.Server.Name == server.Raw && (item.Server.Type == "stdio" || item.Server.Type == "http") {
					declared++
				}
			}
			if found != nil || declared != 1 || server.Raw == "oac_workspace" || tool.Raw == "" ||
				json.Unmarshal([]byte(server.Key), &key) != nil || len(key) != 2 || key[0] != "configured" || key[1] != server.Raw {
				return nil, fmt.Errorf("mcode: ambiguous or undeclared native MCP identity")
			}
			found = &mcpToolIdentity{server: server.Raw, tool: tool.Raw}
		}
	}
	if found == nil {
		return nil, fmt.Errorf("mcode: native MCP identity is missing")
	}
	return found, nil
}

func environmentMCPObservation(update toolUpdate, stage string) (*proto.ToolObservation, error) {
	if update.mcp == nil {
		return nil, nil
	}
	arguments, err := json.Marshal(update.RawInput)
	if err != nil {
		return nil, fmt.Errorf("mcode: invalid native MCP arguments")
	}
	n := &proto.ToolObservation{Kind: "mcp", Status: "in_progress", Server: update.mcp.server, Name: update.mcp.tool,
		Arguments: arguments, Output: json.RawMessage("null"), Error: json.RawMessage("null")}
	if stage == "before" {
		return n, nil
	}
	n.Status = update.Status
	if update.Status == "incomplete" {
		return n, nil
	}
	raw, err := json.Marshal(update.RawOutput)
	var output struct {
		Details *struct {
			Server  string          `json:"server"`
			Tool    string          `json:"tool"`
			MCP     json.RawMessage `json:"mcp"`
			IsError bool            `json:"is_error"`
		} `json:"details"`
	}
	if err != nil || json.Unmarshal(raw, &output) != nil {
		return nil, fmt.Errorf("mcode: invalid native MCP result")
	}
	if output.Details == nil && update.Status == "failed" {
		// Transport failures may have no MCP response. The start registry still
		// identifies the real call, so retain the native failure without guessing.
		n.Error = raw
		return n, nil
	}
	if output.Details == nil || output.Details.Server != n.Server || output.Details.Tool != n.Name ||
		len(output.Details.MCP) == 0 || string(output.Details.MCP) == "null" {
		return nil, fmt.Errorf("mcode: native MCP result identity does not match its call")
	}
	n.Output = output.Details.MCP
	var result struct {
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(n.Output, &result) != nil {
		return nil, fmt.Errorf("mcode: invalid native MCP content")
	}
	if result.IsError || output.Details.IsError {
		n.Status = "failed"
	}
	return n, nil
}

// The existing Session owner calls this after native settlement. No pending
// declared call disappears merely because cancellation omitted a result frame.
func (s *Session) finishEnvironmentMCP() {
	ids := make([]string, 0, len(s.tools))
	for id, call := range s.tools {
		if call.mcp != nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		call := s.tools[id]
		call.Status, call.RawOutput = "incomplete", nil
		_ = s.emitToolStage(call, "after")
		delete(s.tools, id)
		s.completedTools[id] = true
	}
}
