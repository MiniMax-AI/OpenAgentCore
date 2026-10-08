package claudesdk

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestMCPBearerRejectsInvalidCredential(t *testing.T) {
	for _, token := range []string{"", "=", " space", "space ", "has space", "line\r\ninjection", "nul\x00byte", "opaque中文", "middle=padding", "punctuation:invalid"} {
		servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "fixture", ServerURL: "https://example.invalid/mcp", BearerToken: &token}}
		req := proto.PromptRequestPayload{ModelProvider: fixtureProvider(), DisableExecutionEnvironment: true, MCPHTTPServers: &servers, Model: "fixture"}
		if _, _, err := prepareOptions(prepared(t, req)); err == nil || err.Error() != "claudesdk: unsupported HTTPS MCP bearer credential" {
			t.Fatal("invalid bearer accepted or unsafe error returned")
		}
	}
	for _, url := range []string{"http://example.invalid/mcp", "https://example.invalid/mcp#", "https://example.invalid/mcp?", "https://user:secret@example.invalid/mcp"} {
		token := "synthetic-token"
		servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "fixture", ServerURL: url, BearerToken: &token}}
		if err := validateMCP(agent.PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &servers}}); err == nil {
			t.Fatal("unsafe authenticated endpoint accepted")
		}
	}
}
