package mcode

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func testRequest(t *testing.T) proto.PromptRequestPayload {
	t.Helper()
	return proto.PromptRequestPayload{Model: "fixture", SystemPrompt: "Current instructions",
		ModelProvider:               &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://provider.example", APIKey: "fixture-key", ContextWindow: 64000, MaxOutputTokens: 4096},
		DisableExecutionEnvironment: true, DisableSubagents: true, ExecutionControls: &proto.ExecutionControls{TextVerbosity: "medium"}}
}

func prepared(t testing.TB, req proto.PromptRequestPayload) agent.PrepareRequest {
	t.Helper()
	configuration, err := Declaration.Configuration.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	return agent.PrepareRequest{PromptRequestPayload: req, Prepared: configuration, StateKey: "session-state"}
}

// fakeInstall is a view install whose node is cli, a fake native CLI that
// ignores the CLI entry it is given.
func fakeInstall(cli string) viewInstall {
	return viewInstall{node: cli, cli: "/opt/mcode-harness/native/cli.js", bridge: "/opt/mcode-harness/bridge.mjs", assets: "/opt/mcode-harness/native/assets"}
}

// helperInstall is the fake install whose CLI runs scenario of
// TestMCodeProcess. With record, the CLI records each request method there.
func helperInstall(t *testing.T, scenario, record string) viewInstall {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	cli := filepath.Join(t.TempDir(), "mcode")
	script := "#!/bin/sh\nexport OAC_TEST_MCODE_HELPER=" + scenario + "\nexport OAC_TEST_MCODE_RECORD=" + quote(record) + "\nexec " + quote(exe) + " -test.run=^TestMCodeProcess$ -- \"$@\"\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return fakeInstall(cli)
}

// hostSession is a Session whose home has the same path on this host and in
// the view, so the view Executor runs on this host as it runs in a view.
func hostSession(t *testing.T, mcp ...agent.MCPBinding) agent.ViewSession {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, agent.ViewWorkName), 0o700); err != nil {
		t.Fatal(err)
	}
	return agent.ViewSession{Home: agent.ViewDir{Host: home, View: home}, MCP: mcp, Launch: clirunner.Start, Spawn: clirunner.Start,
		StopMCP: func(context.Context, []string) error { return nil }}
}

// hostExecutor prepares req through install's view Executor in session;
// cleanup closes the Executor and reaps its CLI.
func hostExecutor(t *testing.T, ctx context.Context, install viewInstall, req agent.PrepareRequest, session agent.ViewSession) (*executor, error) {
	t.Helper()
	value, err := install.executor(ctx, req, session)
	if value == nil {
		return nil, err
	}
	e := value.(*executor)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Close(cleanup); err != nil {
			t.Error("CLI was not reaped:", err)
		}
	})
	return e, err
}

// prepareExecutor prepares req through install with environment none.
func prepareExecutor(t *testing.T, ctx context.Context, install viewInstall, req proto.PromptRequestPayload) (*executor, error) {
	t.Helper()
	return hostExecutor(t, ctx, install, prepared(t, req), hostSession(t))
}

// startTurn prepares an Executor for req and starts input as its Turn run.
func startTurn(t *testing.T, ctx context.Context, install viewInstall, req proto.PromptRequestPayload, run string, input proto.MessageInput, out chan<- proto.Envelope) (*Session, error) {
	t.Helper()
	e, err := prepareExecutor(t, ctx, install, req)
	if err != nil {
		return nil, err
	}
	turn, err := e.StartTurn(ctx, run, input, out)
	if err != nil {
		return nil, err
	}
	return turn.(*Session), nil
}

// helperSession starts a Turn of the scenario's native CLI, resuming its
// native session when resume is set.
func helperSession(t *testing.T, scenario string, resume bool) (*Session, <-chan proto.Envelope) {
	t.Helper()
	req := testRequest(t)
	if resume {
		req.AgentSessionID = "native-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	out := make(chan proto.Envelope, 32)
	session, err := startTurn(t, ctx, helperInstall(t, scenario, ""), req, "run-1", proto.TextInput("Hello"), out)
	if err != nil {
		t.Fatal(err)
	}
	return session, out
}

func TestSessionStreamsCurrentTurnAndResumes(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "resume"}[resume], func(t *testing.T) {
			_, out := helperSession(t, "happy", resume)
			// Each ACP message is one identified message: its deltas, then its
			// completion snapshot when the next message or the Turn ends it.
			var messages []string
			tools := map[string]int{}
			doneCount := 0
			for event := range out {
				switch event.Type {
				case proto.TypeError:
					t.Fatalf("error: %s", event.Payload)
				case proto.TypeOutputMessage:
					var p proto.OutputMessagePayload
					_ = json.Unmarshal(event.Payload, &p)
					text := "<nil>"
					if p.Text != nil {
						text = *p.Text
					}
					messages = append(messages, p.ID+" "+p.Status+" "+text)
				case proto.TypeDelta:
					var p proto.DeltaPayload
					_ = json.Unmarshal(event.Payload, &p)
					messages = append(messages, p.ItemID+" delta "+p.Delta)
				case proto.TypeToolCall:
					var p proto.ToolCallPayload
					_ = json.Unmarshal(event.Payload, &p)
					tools[p.Stage]++
				case proto.TypeDone:
					doneCount++
					var p proto.DonePayload
					_ = json.Unmarshal(event.Payload, &p)
					if p.Metadata[proto.DoneMetaAgentSessionID] != "native-1" {
						t.Fatalf("done = %#v", p)
					}
					if p.Usage.InputTokens != 0 || p.Usage.OutputTokens != 0 || p.Usage.CostUSD != 0 {
						t.Fatalf("context occupancy reported as usage: %#v", p.Usage)
					}
				}
			}
			want := []string{"m1 in_progress <nil>", "m1 delta Hello ", "m1 delta world", "m1 completed Hello world", "m2 in_progress <nil>", "m2 delta !", "m2 completed !"}
			if !slices.Equal(messages, want) || doneCount != 1 || tools["before"] != 1 || tools["after"] != 1 {
				t.Fatalf("messages=%q done=%d tools=%v", messages, doneCount, tools)
			}
		})
	}
}

// Harnesses run unattended: native asks are declined in the adapter and the turn completes.
func TestSessionDeclinesNativeAsks(t *testing.T) {
	_, out := helperSession(t, "unattended", false)
	done := 0
	for event := range out {
		switch event.Type {
		case proto.TypeError:
			t.Fatalf("error: %s", event.Payload)
		case proto.TypeDone:
			done++
		}
	}
	if done != 1 {
		t.Fatalf("done=%d", done)
	}
}

func TestResumeSelectsModelWhenNativeSelectorIsMissing(t *testing.T) {
	_, out := helperSession(t, "resume-model", true)
	completed := false
	for event := range out {
		if event.Type == proto.TypeError {
			t.Fatalf("resume failed: %s", event.Payload)
		}
		if event.Type == proto.TypeDone {
			completed = true
		}
	}
	if !completed {
		t.Fatal("resume did not complete")
	}
}

func TestSessionFailuresAreReported(t *testing.T) {
	for _, scenario := range []string{"malformed", "exit", "unknown-model"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if e, err := prepareExecutor(t, ctx, helperInstall(t, scenario, ""), testRequest(t)); err == nil || e != nil {
				t.Fatal("preparation failure was not reported", err)
			}
		})
	}
	t.Run("rpc-error", func(t *testing.T) {
		_, out := helperSession(t, "rpc-error", false)
		reported := false
		for event := range out {
			if event.Type == proto.TypeError {
				reported = true
			}
		}
		if !reported {
			t.Fatal("failure was not emitted")
		}
	})
}

func TestPreparationCancellationStopsWaitingCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	if e, err := prepareExecutor(t, ctx, helperInstall(t, "hang", ""), testRequest(t)); err == nil || e != nil {
		t.Fatal("cancelled preparation retained the CLI", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("cancel hung")
	}
}

func TestMCodeProcess(t *testing.T) {
	scenario := os.Getenv("OAC_TEST_MCODE_HELPER")
	if scenario == "" {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	send := func(value any) {
		if err := encoder.Encode(value); err != nil {
			os.Exit(2)
		}
	}
	update := func(kind string, fields map[string]any) {
		fields["sessionUpdate"] = kind
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "native-1", "update": fields}})
	}
	scanner := bufio.NewScanner(os.Stdin)
	var promptID json.RawMessage
	prompts, configurations := 0, 0
	for scanner.Scan() {
		var frame rpcFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			os.Exit(3)
		}
		if scenario == "malformed" {
			os.Stdout.WriteString("invalid JSON\n")
			os.Exit(0)
		}
		if scenario == "exit" {
			os.Exit(1)
		}
		if scenario == "hang" {
			continue
		}
		if record := os.Getenv("OAC_TEST_MCODE_RECORD"); record != "" {
			f, err := os.OpenFile(record, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(10)
			}
			_, _ = f.WriteString(frame.Method + "\n")
			_ = f.Close()
			if frame.Method == "session/new" || frame.Method == "session/load" {
				cwd, err := os.Getwd()
				if err != nil || os.WriteFile(record+".cwd", []byte(cwd), 0600) != nil {
					os.Exit(11)
				}
				if os.WriteFile(record+".session", frame.Params, 0600) != nil {
					os.Exit(11)
				}
			}
		}
		result := any(map[string]any{})
		switch frame.Method {
		case "initialize":
			if scenario == "unattended" && strings.Contains(string(frame.Params), "elicitation") {
				os.Exit(7)
			}
			result = map[string]any{"protocolVersion": 1, "_meta": map[string]any{"oac/mcp-lifecycle": map[string]int{"version": 2}}}
			if scenario == "unpatched-mcp" {
				result = map[string]any{"protocolVersion": 1}
			}
			if scenario == "old-mcp-lifecycle" {
				result = map[string]any{"protocolVersion": 1, "_meta": map[string]any{"oac/mcp-lifecycle": map[string]int{"version": 1}}}
			}
		case "session/new", "session/load":
			if strings.HasPrefix(scenario, "prepared-mcp-") {
				if writeMCPRegistry(os.Getenv("MINIMAX_DATA_DIR"), mcpRegistryEntry("proof.server", "proof_server", "read.status", "read_status"), mcpRegistryEntry("late.server", "late_server", "read.status", "read_status"), mcpRegistryEntry("remote", "remote", "read.status", "read_status")) != nil {
					os.Exit(12)
				}
			}
			if frame.Method == "session/load" {
				update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": "OLD HISTORY"}})
			}
			model := "m:custom_provider%3Aoac:fixture:v:"
			if scenario == "unknown-model" {
				model = "m:minimax:native:u"
			}
			result = map[string]any{"sessionId": "native-1", "configOptions": []map[string]any{{"id": "model", "options": []map[string]string{{"value": model}}}}}
			if scenario == "resume-model" {
				result = map[string]any{"configOptions": []map[string]any{{"id": "permissionMode"}}}
			}
		case "session/set_config_option":
			configurations++
			if scenario == "executor-backpressure" && configurations > 1 {
				send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Result: json.RawMessage("{}")})
				time.Sleep(time.Minute)
				os.Exit(0)
			}
			if scenario == "executor-preexit" && configurations > 1 {
				os.Exit(0)
			}
			var params map[string]string
			_ = json.Unmarshal(frame.Params, &params)
			if params["configId"] == "model" && params["value"] != "m:custom_provider%3Aoac:fixture:v:" {
				os.Exit(4)
			}
		case "session/prompt":
			var input struct {
				Prompt []map[string]string `json:"prompt"`
			}
			_ = json.Unmarshal(frame.Params, &input)
			if len(input.Prompt) != 2 {
				os.Exit(9)
			}
			if strings.HasPrefix(scenario, "executor") {
				prompts++
				promptID = frame.ID
				if scenario == "executor-exit" {
					os.Exit(0)
				}
				if input.Prompt[0]["text"] == "wait" {
					update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": "ready"}})
					continue
				}
				update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": input.Prompt[0]["text"]}})
				update("tool_call", map[string]any{"toolCallId": "repeated-call", "name": "mcp__oac_workspace__workspace_bash", "status": "in_progress", "rawInput": map[string]any{"command": "true"}})
				update("tool_call_update", map[string]any{"toolCallId": "repeated-call", "status": "completed"})
				raw, _ := json.Marshal(map[string]string{"stopReason": "end_turn"})
				send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Result: raw})
				// These frames precede the next control barrier on the wire and
				// must never become the following Turn's output.
				for range 100 {
					update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": "OLD"}})
				}
				continue
			}
			if strings.HasPrefix(scenario, "prepared-mcp-") && input.Prompt[0]["text"] != "next" {
				promptID = frame.ID
				if input.Prompt[0]["text"] == "next-wait" {
					update("agent_message_chunk", map[string]any{"messageId": "next-ready", "content": map[string]string{"type": "text", "text": "next turn ready"}})
					continue
				}
				update("tool_call", map[string]any{"toolCallId": "native-call", "name": "mcp__proof_server__read_status", "status": "in_progress", "rawInput": map[string]any{}})
				if strings.HasPrefix(scenario, "prepared-mcp-prior-") {
					output := mcpNativeResult("proof.server", "read.status", true)
					output["details"].(map[string]any)["oac_response_received"] = scenario == "prepared-mcp-prior-server-error"
					output["details"].(map[string]any)["mcp"].(map[string]any)["_meta"] = map[string]any{"oac_response_received": true}
					update("tool_call_update", map[string]any{"toolCallId": "native-call", "status": "completed", "rawOutput": output})
					// Another call's genuine reply must not clear the first call's owner.
					update("tool_call", map[string]any{"toolCallId": "other-call", "name": "mcp__proof_server__read_status", "status": "in_progress", "rawInput": map[string]any{}})
					update("tool_call_update", map[string]any{"toolCallId": "other-call", "status": "completed", "rawOutput": mcpNativeResult("proof.server", "read.status", false)})
					update("agent_message_chunk", map[string]any{"messageId": "ready", "content": map[string]string{"type": "text", "text": "failures projected"}})
				}
				continue
			}
			if scenario == "steering" || scenario == "steer-rejected" || scenario == "steer-lost" || scenario == "cancel-wait" {
				promptID = frame.ID
				update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": "ready"}})
				continue
			}
			if scenario == "many-frames" || scenario == "prepared" {
				for range 100 {
					update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": "x"}})
				}
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			if scenario == "rpc-error" {
				send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32603, Message: "Fixture provider unavailable"}})
				continue
			}
			if scenario == "unattended" {
				promptID = frame.ID
				send(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{"sessionId": "native-1", "toolCall": map[string]any{"toolCallId": "tool-1", "name": "Bash", "title": "Run fixture", "rawInput": map[string]any{"command": "echo fixture"}}, "options": []map[string]string{{"optionId": "once", "kind": "allow_once"}, {"optionId": "deny", "kind": "reject_once"}}}})
				continue
			}
			update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": "Hello "}})
			update("agent_message_chunk", map[string]any{"messageId": "m1", "content": map[string]string{"type": "text", "text": "world"}})
			update("tool_call", map[string]any{"toolCallId": "tool-1", "name": "mcp__oac_workspace__workspace_bash", "status": "in_progress", "rawInput": map[string]any{"command": "cat fixture.txt"}})
			for range 2 {
				update("tool_call_update", map[string]any{"toolCallId": "tool-1", "status": "completed", "rawOutput": "fixture"})
			}
			update("agent_message_chunk", map[string]any{"messageId": "m2", "content": map[string]string{"type": "text", "text": "!"}})
			update("usage_update", map[string]any{"used": 2000, "size": 64000, "cost": map[string]any{"amount": 2, "currency": "USD"}})
			result = map[string]string{"stopReason": "end_turn"}
		case "mcode/session/delegation/stop":
			result = map[string]any{"receipt": map[string]any{"failedSessionIds": []string{}}}
		case "session/cancel":
			if scenario == "prepared-mcp-lifecycle" {
				update("tool_call_update", map[string]any{"toolCallId": "native-call", "status": "failed", "rawOutput": mcpNativeResult("proof.server", "read.status", true)})
				update("tool_call", map[string]any{"toolCallId": "late-call", "name": "mcp__late_server__read_status", "status": "in_progress", "rawInput": map[string]any{}})
				update("tool_call_update", map[string]any{"toolCallId": "late-call", "status": "failed", "rawOutput": mcpNativeResult("late.server", "read.status", true)})
				update("tool_call", map[string]any{"toolCallId": "http-call", "name": "mcp__remote__read_status", "status": "failed", "rawInput": map[string]any{}, "rawOutput": mcpNativeResult("remote", "read.status", true)})
			}
			raw, _ := json.Marshal(map[string]string{"stopReason": "cancelled"})
			send(rpcFrame{JSONRPC: "2.0", ID: promptID, Result: raw})
			continue
		case "oac/session/mcp/disconnect":
			if record := os.Getenv("OAC_TEST_MCODE_RECORD"); record != "" {
				if os.WriteFile(record+".disconnect", frame.Params, 0600) != nil {
					os.Exit(13)
				}
			}
		case "mcode/session/steer":
			if scenario == "executor-steer-unknown" {
				send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32000, Message: "Unknown input outcome"}})
				continue
			}
			if scenario == "steer-lost" {
				os.Exit(0)
			}
			if scenario == "steer-rejected" {
				send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32602, Message: "inactive"}})
				continue
			}
			raw, _ := json.Marshal(map[string]string{"turnId": "native-turn", "mode": "steered"})
			send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Result: raw})
			update("agent_message_chunk", map[string]any{"messageId": "m2", "content": map[string]string{"type": "text", "text": "-steered"}})
			raw, _ = json.Marshal(map[string]string{"stopReason": "end_turn"})
			send(rpcFrame{JSONRPC: "2.0", ID: promptID, Result: raw})
			continue
		case "":
			if string(frame.ID) == `"permission-1"` {
				var reply struct {
					Outcome struct {
						Outcome string `json:"outcome"`
					} `json:"outcome"`
				}
				if json.Unmarshal(frame.Result, &reply) != nil || reply.Outcome.Outcome != "cancelled" {
					os.Exit(5)
				}
				send(map[string]any{"jsonrpc": "2.0", "id": "question-1", "method": "elicitation/create", "params": map[string]any{"sessionId": "native-1", "mode": "form", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"region": map[string]any{"type": "string"}}}}})
			} else {
				if frame.Error == nil || frame.Error.Code != -32601 {
					os.Exit(6)
				}
				raw, _ := json.Marshal(map[string]string{"stopReason": "end_turn"})
				send(rpcFrame{JSONRPC: "2.0", ID: promptID, Result: raw})
			}
			continue
		}
		raw, _ := json.Marshal(result)
		send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Result: raw})
	}
	os.Exit(0)
}
