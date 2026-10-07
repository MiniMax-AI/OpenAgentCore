package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// The controlled native response checks adapter ordering. Real MCP initialization
// and model execution are verified separately against the pinned harness.
func TestRequiredMCPWaitsForNativeThreadAndNeverRestartsFailedResume(t *testing.T) {
	for _, mode := range []string{"new ready", "new failed", "resume ready", "resume failed"} {
		t.Run(mode, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			req.ExecutionControls = nil
			servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "docs", ServerURL: "https://docs.example/mcp", Required: true}}
			req.MCPHTTPServers = &servers
			method := "thread/start"
			if strings.HasPrefix(mode, "resume") {
				req.AgentSessionID = "fixture-native-thread"
				method = "thread/resume"
			}
			declarations, _, err := runtimeMCPServers(req)
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "mcp-config.json")
			writeMCPHTTPConfigResponse(t, config, mcpHTTPConfigResponse(declarations))
			t.Setenv("OAC_TEST_PREPARATION_MCP_CONFIG", config)
			gate := filepath.Join(root, "required-initialization")
			t.Setenv("OAC_TEST_PREPARATION_THREAD_GATE", gate)
			e, err := testExecutor(t, "complete", req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			out := make(chan proto.Envelope, 16)
			type started struct {
				turn agent.Turn
				err  error
			}
			result := make(chan started, 1)
			go func() {
				turn, err := e.StartTurn(t.Context(), "required-run", proto.TextInput("actual prompt"), out)
				result <- started{turn, err}
			}()
			waitPreparationMethod(t, root, method)
			time.Sleep(100 * time.Millisecond)
			assertNoTurn := func() {
				t.Helper()
				threads := 0
				for _, frame := range preparationFrames(t, root) {
					if strings.HasPrefix(frame.Method, "turn/") {
						t.Fatal("Turn sent without initialized native thread", frame.Method)
					}
					if strings.HasPrefix(frame.Method, "thread/") {
						threads++
						if frame.Method != method || threads != 1 {
							t.Fatal("failed native initialization retried or replaced history")
						}
					}
				}
			}
			assertNoTurn()
			_, state, _ := strings.Cut(mode, " ")
			if err := os.WriteFile(gate, []byte(state), 0o600); err != nil {
				t.Fatal(err)
			}
			var start started
			select {
			case start = <-result:
			case <-time.After(4 * time.Second):
				t.Fatal("native initialization did not finish")
			}
			if (start.err == nil) != (state == "ready") {
				t.Fatal("native initialization outcome changed", start.err)
			}
			frames := settledFrames(t, start.turn, out)
			if state == "ready" {
				return
			}
			assertNoTurn()
			failed := false
			for _, frame := range frames {
				failed = frame.Type == proto.TypeError || failed
			}
			if !failed {
				t.Fatal("native initialization failure was not reported")
			}
		})
	}
}
