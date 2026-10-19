//go:build linux

package claudesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// TestLiveClaudeSDKNativeSubagent runs one Turn in which the model spawns a native general-purpose Subagent with a per-call model.
func TestLiveClaudeSDKNativeSubagent(t *testing.T) {
	entrypoint, keyFile, proofRoot := os.Getenv("OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"), os.Getenv("OAC_TEST_CLAUDE_SDK_MINIMAX_KEY_FILE"), os.Getenv("OAC_TEST_CLAUDE_SDK_PROOF_DIR")
	endpoint, model := os.Getenv("OAC_TEST_CLAUDE_SDK_BASE_URL"), os.Getenv("OAC_TEST_CLAUDE_SDK_MODEL")
	if entrypoint == "" || keyFile == "" || proofRoot == "" || endpoint == "" || model == "" {
		t.Skip("real SDK/provider acceptance requires explicit entrypoint, key file, proof directory, base URL and model")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(proofRoot, "claude-subagent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: entrypoint, StateDir: filepath.Join(root, "state"), Env: []string{
		"ANTHROPIC_BASE_URL=" + endpoint, "ANTHROPIC_AUTH_TOKEN=" + strings.TrimSpace(string(key)), "ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1", "ANTHROPIC_DEFAULT_SONNET_MODEL=" + model, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + model, "ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 256)
	request := proto.PromptRequestPayload{RunID: uuid.NewString(), ObserveMessages: true, ObserveSubagentIdentities: true,
		Input:        proto.TextInput("Call the Agent tool exactly once with subagent_type \"general-purpose\", model \"sonnet\", name \"helper\" and a prompt asking it to reply with the single word PONG. Then report what it replied."),
		AgentOptions: map[string]any{"model": model}}
	turn, err := startSingleTurn(ctx, config, request, out)
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Cancel(context.Background())
	counts := map[string]int{}
	var coordination []map[string]any
	var events []proto.Envelope
	defer func() {
		encoded, _ := json.MarshalIndent(map[string]any{"counts": counts, "coordination": coordination, "events": events}, "", "  ")
		if bytes.Contains(encoded, bytes.TrimSpace(key)) {
			t.Error("evidence contains the provider key")
			return
		}
		_ = os.WriteFile(filepath.Join(root, "evidence.json"), encoded, 0o600)
		t.Logf("evidence: %s/evidence.json counts=%v", root, counts)
	}()
	for event := range out {
		events = append(events, event)
		counts[event.Type]++
		switch event.Type {
		case proto.TypeError:
			t.Fatalf("native failure: %s", event.Payload)
		case proto.TypeSubagentCoordination:
			var fact map[string]any
			_ = json.Unmarshal(event.Payload, &fact)
			coordination = append(coordination, fact)
		}
	}
	if _, err := turn.AwaitSettlement(ctx); err != nil {
		t.Fatal(err)
	}
	if counts[proto.TypeSubagentIdentity] != 1 || counts[proto.TypeSubagentTurn] < 2 || counts[proto.TypeSubagentItem] == 0 {
		t.Fatal("native Subagent was not observed")
	}
	var spawn map[string]any
	for _, fact := range coordination {
		if fact["kind"] == "create_subagent_call" {
			spawn = fact
		}
	}
	if spawn == nil || spawn["status"] != "completed" || spawn["model"] != "sonnet" {
		t.Fatalf("spawn coordination did not report the requested model: %v", spawn)
	}
}
