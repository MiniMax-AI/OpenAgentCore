package mcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func mcpObservationSession(t *testing.T) (*Session, chan proto.Envelope) {
	t.Helper()
	out := make(chan proto.Envelope, 16)
	s := &Session{ctx: context.Background(), opts: launchOptions{DataDir: t.TempDir()},
		req: proto.PromptRequestPayload{RunID: "run", ObserveToolObservations: true,
			LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "enabled", MCP: []proto.EnvironmentMCP{environmentMCPFixture()}}},
		out: out, tools: map[string]toolUpdate{}, completedTools: map[string]bool{}, active: true, sessionID: "native-session"}
	if err := writeMCPRegistry(s.opts.DataDir, mcpRegistryEntry("proof.server", "proof_server_2", "read.status", "read_status_2")); err != nil {
		t.Fatal(err)
	}
	return s, out
}

func receiveMCPObservation(t *testing.T, out chan proto.Envelope, stage, status string) *proto.ToolObservation {
	t.Helper()
	if len(out) == 0 {
		t.Fatal("MCP observation was not emitted immediately")
	}
	var call proto.ToolCallPayload
	if json.Unmarshal((<-out).Payload, &call) != nil || call.ID != "native-call" || call.Stage != stage || call.Observation == nil {
		t.Fatal("MCP native identity or stage lost")
	}
	n := call.Observation
	if n.Kind != "mcp" || n.Server != "proof.server" || n.Name != "read.status" || n.Status != status {
		t.Fatalf("unexpected MCP observation: %+v", n)
	}
	return n
}

func mcpNativeResult(server, tool string, isError bool) map[string]any {
	return map[string]any{"details": map[string]any{"server": server, "tool": tool, "mcp": map[string]any{
		"content": []map[string]string{{"type": "text", "text": "native result"}}, "isError": isError,
	}}}
}

func TestEnvironmentMCPUsesNativeRegistryBeforeResultAndRetainsErrors(t *testing.T) {
	for _, failure := range []string{"none", "tool", "transport", "public-none", "public-tool", "public-transport"} {
		t.Run(failure, func(t *testing.T) {
			s, out := mcpObservationSession(t)
			if strings.HasPrefix(failure, "public-") {
				usePublicMCP(s)
				failure = strings.TrimPrefix(failure, "public-")
			}
			if err := s.emitTool(toolUpdate{ID: "native-call", Name: "mcp__proof_server_2__read_status_2"}); err != nil {
				t.Fatal(err)
			}
			receiveMCPObservation(t, out, "before", "in_progress")
			// Native startup may precede complete arguments; identity is already
			// authoritative without trying to reverse either collision suffix.
			if err := s.emitTool(toolUpdate{ID: "native-call", Status: "in_progress", RawInput: map[string]any{"value": 7}}); err != nil || len(out) != 0 {
				t.Fatal("duplicate start or rejected argument update", err)
			}
			update := toolUpdate{ID: "native-call", Status: "completed", RawOutput: mcpNativeResult("proof.server", "read.status", failure == "tool")}
			status := "completed"
			if failure != "none" {
				status = "failed"
			}
			if failure == "transport" {
				update.Status, update.RawOutput = "failed", map[string]any{"error": "native transport failed"}
			}
			if err := s.emitTool(update); err != nil {
				t.Fatal(err)
			}
			n := receiveMCPObservation(t, out, "after", status)
			if string(n.Arguments) != `{"value":7}` || (failure == "transport" && !strings.Contains(string(n.Error), "native transport failed")) ||
				(failure != "transport" && !strings.Contains(string(n.Output), "native result")) {
				t.Fatal("native arguments, content or transport failure lost")
			}
			if err := s.emitTool(update); err != nil || len(out) != 0 {
				t.Fatal("duplicate completion replayed")
			}
		})
	}
}

func TestEnvironmentMCPIdentityRequiresUniqueCurrentConfiguredAssignment(t *testing.T) {
	for _, mutation := range []string{"missing-file", "missing-tool", "ambiguous", "foreign-server", "plugin-key", "wrong-key", "bad-version", "invalid-json"} {
		t.Run(mutation, func(t *testing.T) {
			s, out := mcpObservationSession(t)
			entry := mcpRegistryEntry("proof.server", "proof_server_2", "read.status", "read_status_2")
			path := filepath.Join(s.opts.DataDir, "mcp-runtime-names.json")
			var err error
			switch mutation {
			case "missing-file":
				err = os.Remove(path)
			case "missing-tool":
				err = writeMCPRegistry(s.opts.DataDir)
			case "ambiguous":
				err = writeMCPRegistry(s.opts.DataDir, entry, entry)
			case "foreign-server":
				err = writeMCPRegistry(s.opts.DataDir, mcpRegistryEntry("foreign", "proof_server_2", "read.status", "read_status_2"))
			case "plugin-key", "wrong-key":
				entry["key"] = `["plugin","proof.server"]`
				if mutation == "wrong-key" {
					entry["key"] = `["configured","foreign"]`
				}
				err = writeMCPRegistry(s.opts.DataDir, entry)
			case "bad-version":
				err = os.WriteFile(path, []byte(`{"version":2,"servers":[]}`), 0600)
			case "invalid-json":
				err = os.WriteFile(path, []byte(`private-invalid-registry`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.emitTool(toolUpdate{ID: "native-call", Name: "mcp__proof_server_2__read_status_2"}); err == nil || strings.Contains(err.Error(), "private-invalid-registry") || len(out) != 0 {
				t.Fatal("invalid registry became a public MCP call or leaked raw data")
			}
		})
	}
}

func TestEnvironmentMCPResultMustMatchStartAndUnsettledCallsCloseOnce(t *testing.T) {
	for _, change := range []string{"server", "tool", "missing-details", "native-name", "cancel", "public-cancel"} {
		t.Run(change, func(t *testing.T) {
			s, out := mcpObservationSession(t)
			if change == "public-cancel" {
				usePublicMCP(s)
				change = "cancel"
			}
			if err := s.emitTool(toolUpdate{ID: "native-call", Name: "mcp__proof_server_2__read_status_2", RawInput: map[string]any{"key": "value"}}); err != nil {
				t.Fatal(err)
			}
			receiveMCPObservation(t, out, "before", "in_progress")
			update := toolUpdate{ID: "native-call", Status: "completed", RawOutput: mcpNativeResult("proof.server", "read.status", false)}
			switch change {
			case "server":
				update.RawOutput = mcpNativeResult("foreign", "read.status", false)
			case "tool":
				update.RawOutput = mcpNativeResult("proof.server", "other", false)
			case "missing-details":
				update.RawOutput = map[string]any{"content": "cannot-establish-identity"}
			case "native-name":
				update.Name = "mcp__proof_server_2__other"
			}
			if change != "cancel" {
				if err := s.emitTool(update); err == nil || len(out) != 0 {
					t.Fatal("inconsistent result was accepted")
				}
			}
			s.finishEnvironmentMCP()
			n := receiveMCPObservation(t, out, "after", "incomplete")
			if string(n.Arguments) != `{"key":"value"}` || string(n.Output) != "null" {
				t.Fatal("incomplete call lost arguments or invented output")
			}
			s.finishEnvironmentMCP()
			if len(out) != 0 {
				t.Fatal("incomplete observation duplicated")
			}
		})
	}
}

func TestEnvironmentMCPDoesNotPublishInternalWorkspaceUtilities(t *testing.T) {
	s, out := mcpObservationSession(t)
	for _, name := range []string{"workspace_read", "workspace_write", "workspace_edit", "workspace_glob", "workspace_grep"} {
		if err := s.emitTool(toolUpdate{ID: name, Name: "mcp__oac_workspace__" + name, Status: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(out) != 0 {
		t.Fatal("internal workspace utility became a public MCP call")
	}
}

func TestEnvironmentMCPNativeJSONRetainsIntegerPrecision(t *testing.T) {
	s, out := mcpObservationSession(t)
	frames := []string{
		`{"sessionId":"native-session","update":{"sessionUpdate":"tool_call","toolCallId":"native-call","name":"mcp__proof_server_2__read_status_2","rawInput":{"value":9007199254740993}}}`,
		`{"sessionId":"native-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"native-call","status":"completed","rawOutput":{"details":{"server":"proof.server","tool":"read.status","mcp":{"structuredContent":{"value":9007199254740993},"content":[],"isError":false}}}}}`,
	}
	for _, raw := range frames {
		if err := s.handle(rpcFrame{Method: "session/update", Params: json.RawMessage(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	before := receiveMCPObservation(t, out, "before", "in_progress")
	after := receiveMCPObservation(t, out, "after", "completed")
	if !strings.Contains(string(before.Arguments), "9007199254740993") || !strings.Contains(string(after.Output), "9007199254740993") {
		t.Fatal("MCP structured number rounded by native observation decoding")
	}
}

// Both declaration sources must produce the same native observation semantics.
func usePublicMCP(s *Session) {
	s.req.LocalEnvironment.MCP = nil
	s.req.MCPHTTPServers = &[]proto.MCPHTTPServer{{
		ConnectionOrigin: "environment", ServerLabel: "proof.server", ServerURL: "https://mcp.example.test",
	}}
}
