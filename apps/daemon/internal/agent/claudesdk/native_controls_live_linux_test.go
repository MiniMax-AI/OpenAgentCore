//go:build linux

package claudesdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// TestLiveClaudeSDKNativeControls proves, on the real SDK and provider, a whitespace-only message
// and live web search on environment none.
func TestLiveClaudeSDKNativeControls(t *testing.T) {
	entrypoint, keyFile, proofRoot := os.Getenv("OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"), os.Getenv("OAC_TEST_CLAUDE_SDK_MINIMAX_KEY_FILE"), os.Getenv("OAC_TEST_CLAUDE_SDK_PROOF_DIR")
	endpoint, model := os.Getenv("OAC_TEST_CLAUDE_SDK_BASE_URL"), os.Getenv("OAC_TEST_CLAUDE_SDK_MODEL")
	if entrypoint == "" || keyFile == "" || endpoint == "" || model == "" || !filepath.IsAbs(proofRoot) {
		t.Skip("real SDK/provider acceptance requires an entrypoint, a private key file, an endpoint, a model and an absolute proof directory")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(proofRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(proofRoot, "claude-controls-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: entrypoint, StateDir: filepath.Join(root, "state"), Env: []string{
		"ANTHROPIC_BASE_URL=" + endpoint, "ANTHROPIC_AUTH_TOKEN=" + strings.TrimSpace(string(key)),
		"ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + model, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + model, "ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model,
	}}
	run := func(t *testing.T, prompt proto.MessageInput, configure func(*proto.PromptRequestPayload)) (items []string, content, failure string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
		req := proto.PromptRequestPayload{RunID: uuid.NewString(), DisableExecutionEnvironment: true, DisableSubagents: true, Input: prompt,
			ExecutionControls: &proto.ExecutionControls{TextVerbosity: "medium"},
			AgentOptions:      map[string]any{"model": model, "system_prompt": "Answer briefly."}}
		configure(&req)
		out := make(chan proto.Envelope, 64)
		running, err := startSingleTurn(ctx, config, req, out)
		if err != nil {
			t.Fatal(err)
		}
		defer running.Cancel(context.Background())
		for event := range out {
			items = append(items, event.Type)
			switch event.Type {
			case proto.TypeError:
				var payload proto.ErrorPayload
				_ = json.Unmarshal(event.Payload, &payload)
				failure = payload.Error
			case proto.TypeDone:
				var payload proto.DonePayload
				_ = json.Unmarshal(event.Payload, &payload)
				content = payload.Content
			}
		}
		return
	}
	t.Run("whitespace_only_message", func(t *testing.T) {
		items, content, failure := run(t, proto.TextInput(" \n\t"), func(*proto.PromptRequestPayload) {})
		if failure != "" || content == "" {
			t.Fatalf("whitespace-only message failed: items=%v failure=%q", items, failure)
		}
	})
	t.Run("live_search_on_none", func(t *testing.T) {
		items, content, failure := run(t, proto.TextInput("Search the web for the current stable version of Go and report it in one sentence."), func(req *proto.PromptRequestPayload) {
			req.ExecutionControls.WebSearch = &proto.WebSearchControl{Mode: "live", ContextSize: "medium"}
		})
		if failure != "" || content == "" || !slices.Contains(items, proto.TypeToolCall) {
			t.Fatalf("live search failed: items=%v failure=%q", items, failure)
		}
	})
}
