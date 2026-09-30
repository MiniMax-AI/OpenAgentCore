//go:build unix

package claudesdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestClassifiedBridgeFailurePreservesTerminalEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "unconfirmed", "wrong-session", "wrong-result", "success-usage", "cancelled", "unknown", "malformed-code", "after-terminal", "scanner-error", "process-error"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", root)
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=classified-" + mode, "GORACE=atexit_sleep_ms=0"}}
			req := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("hello"), AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 16)
			s, err := NewFactory(config)(ctx, req, out)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel(context.Background())
			var failure proto.ErrorPayload
			var done proto.DonePayload
			usage, errors, dones := 0, 0, 0
			for event := range out {
				switch event.Type {
				case proto.TypeUsage:
					usage++
				case proto.TypeError:
					errors++
					_ = event.DecodePayload(&failure)
				case proto.TypeDone:
					dones++
					_ = event.DecodePayload(&done)
				}
			}
			if mode == "unconfirmed" {
				if _, err := s.(*session).AwaitSettlement(ctx); err == nil {
					t.Fatal("native diagnostic fabricated confirmed Turn settlement")
				}
			}
			want := ""
			if mode == "valid" || mode == "unconfirmed" {
				want = "authentication_error"
			}
			if errors != 1 || dones != 1 || usage != 1 || failure.Code != want || done.Metadata[proto.DoneMetaAgentSessionID] != "native-session" || done.Usage.Raw["claude_sdk_result"] == nil {
				t.Fatalf("lost evidence: %d/%d/%d %+v %+v", errors, dones, usage, failure, done)
			}
		})
	}
}

func runClassifiedFailureHelper(request startRequest, mode string, encode func(bridgeEvent)) {
	mode = strings.TrimPrefix(mode, "classified-")
	encode(bridgeEvent{Type: "input_ready", SessionID: request.Resume})
	raw := strings.ReplaceAll(strings.ReplaceAll(usageFixture, `"subtype":"success"`, `"subtype":"error_during_execution"`), `"is_error":false`, `"is_error":true`)
	if mode == "success-usage" {
		raw = usageFixture
	}
	encode(bridgeEvent{Type: "usage", SessionID: request.Resume, ResultID: "result", Usage: json.RawMessage(raw)})
	e := bridgeEvent{Type: "error", Code: "execution_failed", SessionID: request.Resume, ResultID: "result", EngineErrorCode: json.RawMessage(`"authentication_error"`)}
	switch mode {
	case "wrong-session":
		e.SessionID = "foreign"
	case "wrong-result":
		e.ResultID = "foreign"
	case "cancelled":
		e.Code = "cancelled"
	case "unknown":
		e.EngineErrorCode = json.RawMessage(`"future"`)
	case "malformed-code":
		e.EngineErrorCode = json.RawMessage(`{"secret":"value"}`)
	}
	encode(e)
	if mode == "unconfirmed" {
		confirmed, reusable := false, false
		encode(bridgeEvent{Type: "turn_settled", Confirmed: &confirmed, Reusable: &reusable, Reason: "native_execution_unavailable"})
		os.Exit(0)
	}
	if mode == "process-error" {
		os.Exit(7)
	}
	if mode == "after-terminal" {
		fmt.Fprintln(os.Stdout, `{"type":"delta","delta":"late"}`)
	}
	if mode == "scanner-error" {
		fmt.Fprintln(os.Stdout, strings.Repeat("x", 2*1024*1024))
	}
}
