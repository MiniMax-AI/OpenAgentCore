package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type mcpToolIdentity struct{ server, tool string }

// MiniMax 0.4.12 atomically writes these assignments before exposing tools.
// Read its exact mapping instead of reversing lossy native name normalization.
func (s *Session) environmentMCPIdentity(name string) (*mcpToolIdentity, error) {
	bindings := s.opts.bindings
	if len(bindings) == 0 || !strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "mcp__oac_workspace__") {
		return nil, nil
	}
	raw, err := s.opts.readData("mcp-runtime-names.json")
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
			for _, item := range bindings {
				if item.ServerLabel == server.Raw {
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

func environmentMCPObservation(update toolUpdate, stage string) (*proto.ToolObservation, bool, error) {
	if update.mcp == nil {
		return nil, false, nil
	}
	arguments, err := json.Marshal(update.RawInput)
	if err != nil {
		return nil, false, fmt.Errorf("mcode: invalid native MCP arguments")
	}
	n := &proto.ToolObservation{Kind: "mcp", Status: "in_progress", Server: update.mcp.server, Name: update.mcp.tool,
		Arguments: arguments, Output: json.RawMessage("null"), Error: json.RawMessage("null")}
	if stage == "before" {
		return n, false, nil
	}
	n.Status = update.Status
	if update.Status == "incomplete" {
		return n, false, nil
	}
	raw, err := json.Marshal(update.RawOutput)
	var output struct {
		Details *struct {
			Server           string          `json:"server"`
			Tool             string          `json:"tool"`
			MCP              json.RawMessage `json:"mcp"`
			IsError          bool            `json:"is_error"`
			ResponseReceived bool            `json:"oac_response_received"`
		} `json:"details"`
	}
	if err != nil || json.Unmarshal(raw, &output) != nil {
		return nil, false, fmt.Errorf("mcode: invalid native MCP result")
	}
	if output.Details == nil && update.Status == "failed" {
		// Transport failures may have no MCP response. The start registry still
		// identifies the real call, so retain the native failure without guessing.
		n.Error = raw
		return n, false, nil
	}
	if output.Details == nil || output.Details.Server != n.Server || output.Details.Tool != n.Name ||
		len(output.Details.MCP) == 0 || string(output.Details.MCP) == "null" {
		return nil, false, fmt.Errorf("mcode: native MCP result identity does not match its call")
	}
	n.Output = output.Details.MCP
	var result struct {
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(n.Output, &result) != nil {
		return nil, false, fmt.Errorf("mcode: invalid native MCP content")
	}
	if result.IsError || output.Details.IsError {
		n.Status = "failed"
	}
	return n, output.Details.ResponseReceived, nil
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

// trackMCPCancellation runs before output delivery, which can block. Cancel
// and late native callbacks therefore agree on the same in-flight identities.
func (s *Session) trackMCPCancellation(call toolUpdate, responseReceived bool) {
	if call.mcp == nil {
		return
	}
	stdio := slices.ContainsFunc(s.opts.bindings, func(binding agent.MCPBinding) bool {
		return binding.ServerLabel == call.mcp.server && binding.Transport == "stdio"
	})
	if !stdio {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcpCalls == nil {
		s.mcpCalls = map[string]string{}
	}
	if s.cancelled {
		if s.cancelledMCP == nil {
			s.cancelledMCP = map[string]bool{}
		}
		s.cancelledMCP[call.mcp.server] = true
	}
	// A local timeout is also projected as a terminal tool error. Only the
	// native wrapper's verified SDK reply releases this Turn's remote owner.
	if responseReceived {
		delete(s.mcpCalls, call.ID)
	} else {
		s.mcpCalls[call.ID] = call.mcp.server
	}
}

// captureMCPCancellation is called with mu held before session/cancel is sent.
func (s *Session) captureMCPCancellation() {
	if s.cancelledMCP == nil {
		s.cancelledMCP = map[string]bool{}
	}
	for _, server := range s.mcpCalls {
		s.cancelledMCP[server] = true
	}
}

func (s *Session) settleCancelledMCP() (map[string]bool, error) {
	s.mu.Lock()
	servers := make([]string, 0, len(s.cancelledMCP))
	for server := range s.cancelledMCP {
		servers = append(servers, server)
	}
	s.mu.Unlock()
	if len(servers) == 0 {
		return nil, nil
	}
	slices.Sort(servers)
	if s.opts.stopMCP == nil {
		return nil, fmt.Errorf("mcode: MCP scope settlement is unavailable")
	}
	ctx, cancel := context.WithTimeout(s.process.Context(), 60*time.Second)
	defer cancel()
	if err := s.opts.stopMCP(ctx, servers); err != nil {
		return nil, fmt.Errorf("mcode: MCP scope settlement: %w", err)
	}
	// The sandbox scope closes first. Native then fences the old transport while
	// retaining its configuration, so the next call can reconnect lazily.
	if err := s.call("oac/session/mcp/disconnect", map[string]any{"sessionId": s.sessionID, "servers": servers}, nil, false); err != nil {
		return nil, err
	}
	stopped := make(map[string]bool, len(servers))
	for _, server := range servers {
		stopped[server] = true
	}
	return stopped, nil
}
