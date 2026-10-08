package claudesdk

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestEnvironmentMCPObservationsUseInstalledDeclarations(t *testing.T) {
	start := startRequest{Workspace: &workspaceProfile{MCP: []environmentMCPServer{{mcpHTTPServer: mcpHTTPServer{ServerLabel: "installed"}}}}}
	state := mcpState{calls: map[string]proto.ToolObservation{}}
	observation := proto.ToolObservation{Kind: "mcp", Name: "echo", Server: "installed", Status: "in_progress", Arguments: json.RawMessage(`{}`), Output: json.RawMessage(`null`), Error: json.RawMessage(`null`)}
	var emitted []proto.ToolCallPayload
	emit := func(_ string, payload any) { emitted = append(emitted, payload.(proto.ToolCallPayload)) }
	if err := state.receive(bridgeEvent{ID: "native-call", Stage: "before", Observation: &observation}, start, emit); err != nil {
		t.Fatal(err)
	}
	state.close(emit)
	if len(emitted) != 2 || emitted[1].ID != "native-call" || emitted[1].Observation.Status != "incomplete" {
		t.Fatal("interrupted environment call lost identity")
	}
	observation.Server = "undeclared"
	if err := state.receive(bridgeEvent{ID: "other", Stage: "before", Observation: &observation}, start, emit); err == nil {
		t.Fatal("undeclared observation accepted")
	}
}
