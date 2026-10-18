package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// fakePi writes a launcher that runs the test binary as the Pi child, because
// the Executor intentionally passes only an allowlisted environment.
func fakePi(t *testing.T, extraEnv ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Pi launcher uses a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "pi")
	prefix := "GO_WANT_PI_RPC_HELPER=1 "
	for _, value := range extraEnv {
		prefix += value + " "
	}
	script := "#!/bin/sh\n" + prefix + "exec " + os.Args[0] + " -test.run=^TestExecutorHelper$\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("fake pi: %v", err)
	}
	t.Setenv("OAC_RUNTIME_PI_BIN", path)
	return path
}

func prepareTestExecutor(t *testing.T) agent.Executor {
	t.Helper()
	return prepareTestExecutorWith(t, nil)
}

func prepareTestExecutorWith(t *testing.T, harnessConfig map[string]any) agent.Executor {
	t.Helper()
	factory := NewExecutorFactory(t.TempDir(), t.TempDir())
	options := map[string]any{
		"model": "test-model",
		"model_provider": map[string]any{
			"protocol": "chat_completions",
			"base_url": "http://127.0.0.1:1/v1",
			"api_key":  "test-key",
		},
	}
	if harnessConfig != nil {
		options["harness_config"] = harnessConfig
	}
	executor, err := factory(context.Background(), proto.PromptRequestPayload{
		AgentKind:        "pi",
		AgentOptions:     options,
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceRoot: t.TempDir()},
		ObserveMessages:  true,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return executor
}

func drain(out <-chan proto.Envelope) {
	go func() {
		for range out {
		}
	}()
}

func TestExecutorRunsOneTextTurn(t *testing.T) {
	fakePi(t)
	executor := prepareTestExecutor(t)
	defer executor.Close(context.Background())

	out := make(chan proto.Envelope, 32)
	turn, err := executor.StartTurn(context.Background(), "run-1", proto.TextInput("hello"), out)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	var content string
	var sawDelta, sawOutput, sawDone bool
	for envelope := range out {
		switch envelope.Type {
		case proto.TypeDelta:
			sawDelta = true
		case proto.TypeOutputMessage:
			sawOutput = true
		case proto.TypeDone:
			var payload proto.DonePayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				t.Fatalf("done payload: %v", err)
			}
			content = payload.Content
			sawDone = true
		}
	}
	if !sawDelta || !sawOutput || !sawDone {
		t.Fatalf("missing frames: delta=%v output=%v done=%v", sawDelta, sawOutput, sawDone)
	}
	if content != "Hello" {
		t.Fatalf("done content = %q", content)
	}
	settlement, err := turn.AwaitSettlement(context.Background())
	if err != nil {
		t.Fatalf("settlement: %v", err)
	}
	if !settlement.Reusable {
		t.Fatalf("expected reusable settlement, got %+v", settlement)
	}
}

func TestModelErrorFailsTheTurn(t *testing.T) {
	fakePi(t, "GO_WANT_PI_MODEL_ERROR=1")
	executor := prepareTestExecutor(t)
	defer executor.Close(context.Background())

	out := make(chan proto.Envelope, 32)
	turn, err := executor.StartTurn(context.Background(), "run-1", proto.TextInput("hello"), out)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	var sawError bool
	for envelope := range out {
		if envelope.Type == proto.TypeError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("a failed model response must emit an error frame")
	}
	if state, err := turn.AwaitSettlement(context.Background()); err == nil || state.Reusable {
		t.Fatalf("failed Turn settled as reusable: %+v %v", state, err)
	}
}

func TestSteerWaitsForNativeEntry(t *testing.T) {
	fakePi(t)
	executor := prepareTestExecutor(t)
	defer executor.Close(context.Background())

	out := make(chan proto.Envelope, 32)
	turn, err := executor.StartTurn(context.Background(), "run-1", proto.TextInput("hello"), out)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	drain(out)
	written := 0
	payload := proto.PromptSteerPayload{Input: proto.TextInput("follow up")}
	if err := turn.SteerWithReceipt(context.Background(), payload, func() { written++ }); err != nil {
		t.Fatalf("SteerWithReceipt: %v", err)
	}
	if written != 1 {
		t.Fatalf("written callback ran %d times", written)
	}
}

func TestSteerRejectsAmbiguousEntries(t *testing.T) {
	fakePi(t, "GO_WANT_PI_AMBIGUOUS=1")
	executor := prepareTestExecutor(t)
	defer executor.Close(context.Background())

	out := make(chan proto.Envelope, 32)
	turn, err := executor.StartTurn(context.Background(), "run-1", proto.TextInput("hello"), out)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	drain(out)
	payload := proto.PromptSteerPayload{Input: proto.TextInput("follow up")}
	err = turn.SteerWithReceipt(context.Background(), payload, nil)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

// TestExecutorHelper stands in for the Pi process. It keeps a small entry tree
// so receipt reads have something authoritative to return.
func TestExecutorHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PI_RPC_HELPER") != "1" {
		return
	}
	ambiguous := os.Getenv("GO_WANT_PI_AMBIGUOUS") == "1"
	modelError := os.Getenv("GO_WANT_PI_MODEL_ERROR") == "1"
	active := false
	entries := []map[string]any{{"type": "message", "id": "e0", "message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "seed"}}}}}
	next := 1
	appendUser := func(text string) {
		entries = append(entries, map[string]any{"type": "message", "id": "e" + strconv.Itoa(next), "message": map[string]any{"role": "user", "content": text}})
		next++
	}
	leaf := func() string {
		last, _ := entries[len(entries)-1]["id"].(string)
		return last
	}
	page := func(since string) map[string]any {
		start := 0
		for i, item := range entries {
			if item["id"] == since {
				start = i + 1
				break
			}
		}
		return map[string]any{"entries": entries[start:], "leafId": leaf()}
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Since   string `json:"since"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		switch request.Type {
		case "prompt":
			respond(request.ID, request.Type, map[string]any{"disposition": "started"})
			appendUser(request.Message)
			writeEvent(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "Hello"}})
			if request.Message == "wait" {
				active = true
				continue
			}
			if modelError {
				writeEvent(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []map[string]any{}, "stopReason": "error", "errorMessage": "invalid api key"}})
			} else {
				writeEvent(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "Hello"}}}})
			}
			writeEvent(map[string]any{"type": "agent_settled", "aborted": false})
		case "steer":
			respond(request.ID, request.Type, map[string]any{"disposition": "queued"})
			appendUser(request.Message)
			if ambiguous {
				appendUser(request.Message + " again")
			}
		case "get_entries":
			respond(request.ID, request.Type, page(request.Since))
		case "abort":
			respond(request.ID, request.Type, map[string]any{})
			if active {
				active = false
				writeEvent(map[string]any{"type": "agent_settled", "aborted": true})
			}
		default:
			respond(request.ID, request.Type, map[string]any{})
		}
	}
	os.Exit(0)
}

func respond(id, command string, data map[string]any) {
	writeEvent(map[string]any{"id": id, "type": "response", "command": command, "success": true, "data": data})
}

func writeEvent(value map[string]any) {
	line, err := json.Marshal(value)
	if err != nil {
		return
	}
	_, _ = os.Stdout.Write(append(line, '\n'))
}
