//go:build linux

package claudesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// liveSubagentConfig reads the private provider settings of the real-provider Subagent tests.
func liveSubagentConfig(t *testing.T) (config Config, key []byte, root string) {
	entrypoint, keyFile, proofRoot := os.Getenv("OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"), os.Getenv("OAC_TEST_CLAUDE_SDK_MINIMAX_KEY_FILE"), os.Getenv("OAC_TEST_CLAUDE_SDK_PROOF_DIR")
	endpoint, model := os.Getenv("OAC_TEST_CLAUDE_SDK_BASE_URL"), os.Getenv("OAC_TEST_CLAUDE_SDK_MODEL")
	if entrypoint == "" || keyFile == "" || proofRoot == "" || endpoint == "" || model == "" {
		t.Skip("real SDK/provider acceptance requires explicit entrypoint, key file, proof directory, base URL and model")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	key = bytes.TrimSpace(key)
	root, err = os.MkdirTemp(proofRoot, "claude-subagent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", root)
	return Config{Entrypoint: entrypoint, StateDir: filepath.Join(root, "state"), Env: []string{
		"ANTHROPIC_BASE_URL=" + endpoint, "ANTHROPIC_AUTH_TOKEN=" + string(key), "ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1", "ANTHROPIC_DEFAULT_SONNET_MODEL=" + model, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + model, "ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model,
	}}, key, root
}

// saveEvidence stores the observed events of a live run, unless they would contain the provider key.
func saveEvidence(t *testing.T, root string, key []byte, evidence any) {
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	if bytes.Contains(encoded, key) {
		t.Error("evidence contains the provider key")
		return
	}
	_ = os.WriteFile(filepath.Join(root, "evidence.json"), encoded, 0o600)
	t.Logf("evidence: %s/evidence.json", root)
}

// TestLiveClaudeSDKNativeSubagent runs one Turn in which the model spawns a native general-purpose Subagent with a per-call model.
func TestLiveClaudeSDKNativeSubagent(t *testing.T) {
	config, key, root := liveSubagentConfig(t)
	model := os.Getenv("OAC_TEST_CLAUDE_SDK_MODEL")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 256)
	request := proto.PromptRequestPayload{RunID: uuid.NewString(), ObserveSubagentIdentities: true,
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
		saveEvidence(t, root, key, map[string]any{"counts": counts, "coordination": coordination, "events": events})
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

// TestLiveClaudeSDKSubagentFunctionCall runs one Turn in which a native Subagent calls the Agent's function. The call reaches Core with the child's
// identity after the child's facts, the submitted result is confirmed by the child's own native tool result, and the Turn reports no root tool call for it.
func TestLiveClaudeSDKSubagentFunctionCall(t *testing.T) {
	config, key, root := liveSubagentConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 256)
	request := proto.PromptRequestPayload{RunID: uuid.NewString(), ObserveSubagentIdentities: true,
		FunctionTools: []proto.FunctionTool{{Name: "lookup", Description: "Returns the secret code of a topic.", Parameters: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string"}},"required":["topic"]}`)}},
		Input:         proto.TextInput("Call the Agent tool exactly once with subagent_type \"general-purpose\", name \"helper\" and a prompt telling it to call the mcp__functions__lookup tool with topic \"sky\" and reply with exactly the code it returned. Then report that code."),
		AgentOptions:  map[string]any{"model": os.Getenv("OAC_TEST_CLAUDE_SDK_MODEL")}}
	turn, err := startSingleTurn(ctx, config, request, out)
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Cancel(context.Background())
	submitter := turn.(agent.FunctionResultSubmitter)
	var events []proto.Envelope
	var call proto.FunctionCallPayload
	var tools, subagentTools, factsBefore int
	submitted := make(chan error, 1)
	defer func() { saveEvidence(t, root, key, events) }()
	for event := range out {
		events = append(events, event)
		switch event.Type {
		case proto.TypeError:
			t.Fatalf("native failure: %s", event.Payload)
		case proto.TypeSubagentIdentity, proto.TypeSubagentTurn:
			if call.CallID == "" {
				factsBefore++
			}
		case proto.TypeToolCall:
			tools++
		case proto.TypeSubagentItem:
			if bytes.Contains(event.Payload, []byte(`"kind":"function"`)) {
				subagentTools++
			}
		case proto.TypeFunctionCall:
			if err := event.DecodePayload(&call); err != nil {
				t.Fatal(err)
			}
			text := "CODE-4821"
			result := proto.FunctionResultPayload{DeliveryID: "delivery", CallID: call.CallID, Success: true, Content: []proto.InputContent{{Type: "input_text", Text: &text}}}
			go func() { submitted <- submitter.SubmitFunctionResult(ctx, result) }()
		}
	}
	if _, err := turn.AwaitSettlement(ctx); err != nil {
		t.Fatal(err)
	}
	if call.NativeID == "" || call.TurnID == "" || factsBefore < 2 || tools != 0 || subagentTools == 0 {
		t.Fatalf("child call %+v, facts before it %d, root tool calls %d, child function items %d", call, factsBefore, tools, subagentTools)
	}
	if err := <-submitted; err != nil {
		t.Fatalf("child result was not confirmed: %v", err)
	}
}

// TestLiveClaudeSDKSubagentFunctionCallWithdrawn cancels the Turn while a Subagent's function call waits. Native stops the child, and the call is withdrawn.
func TestLiveClaudeSDKSubagentFunctionCallWithdrawn(t *testing.T) {
	config, key, root := liveSubagentConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 256)
	request := proto.PromptRequestPayload{RunID: uuid.NewString(), ObserveSubagentIdentities: true,
		FunctionTools: []proto.FunctionTool{{Name: "lookup", Description: "Returns the secret code of a topic.", Parameters: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string"}},"required":["topic"]}`)}},
		Input:         proto.TextInput("Call the Agent tool exactly once with subagent_type \"general-purpose\" and a prompt telling it to call the mcp__functions__lookup tool with topic \"sky\" and reply with the code it returned."),
		AgentOptions:  map[string]any{"model": os.Getenv("OAC_TEST_CLAUDE_SDK_MODEL")}}
	turn, err := startSingleTurn(ctx, config, request, out)
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Cancel(context.Background())
	var events []proto.Envelope
	var call, withdrawn proto.FunctionCallWithdrawnPayload
	var pending proto.FunctionCallPayload
	defer func() { saveEvidence(t, root, key, events) }()
	for event := range out {
		events = append(events, event)
		switch event.Type {
		case proto.TypeFunctionCall:
			if err := event.DecodePayload(&pending); err != nil {
				t.Fatal(err)
			}
			go turn.Cancel(ctx)
		case proto.TypeFunctionCallWithdrawn:
			if err := event.DecodePayload(&withdrawn); err != nil {
				t.Fatal(err)
			}
		}
	}
	call = proto.FunctionCallWithdrawnPayload{CallID: pending.CallID, NativeID: pending.NativeID, TurnID: pending.TurnID}
	if pending.NativeID == "" || withdrawn != call {
		t.Fatalf("call %+v was not withdrawn: %+v", pending, withdrawn)
	}
}
