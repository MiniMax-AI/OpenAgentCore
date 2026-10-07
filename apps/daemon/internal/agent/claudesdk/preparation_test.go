//go:build unix

package claudesdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestPreparationWaitsForReceiptAndRetainsConfiguration(t *testing.T) {
	config := preparationFixture(t, "delayed-ready")
	req := preparationRequest()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan agent.Executor, 1)
	failed := make(chan error, 1)
	go func() {
		e, err := NewExecutorFactory(config)(ctx, req)
		if err != nil {
			failed <- err
			return
		}
		result <- e
	}()
	raw := waitPreparationFile(t, filepath.Join(config.StateDir, "prepare.json"))
	select {
	case <-result:
		t.Fatal("preparation returned before its native receipt")
	case err := <-failed:
		t.Fatal(err)
	default:
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var resource agent.Executor
	select {
	case resource = <-result:
	case err := <-failed:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	e := resource.(*executor)
	defer e.Close(context.Background())
	pid := e.base.process.Cmd.Process.Pid
	if _, err := os.Stat(filepath.Join(config.StateDir, "start.json")); !os.IsNotExist(err) {
		t.Fatal("preparation submitted input")
	}
	var frozen startRequest
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.Model != "fixture" || frozen.Resume != "native-session" || frozen.Workspace == nil {
		t.Fatal("configuration-only request was not retained")
	}
	config.Env[0] = "HTTPS_PROXY=http://changed.example"
	config.Workspace.Directory = "/changed"
	req.Model = "changed"
	req.AgentSessionID = "changed"
	out := make(chan proto.Envelope, 16)
	operation, stopOperation := context.WithCancel(ctx)
	turn, err := e.StartTurn(operation, "actual-run", proto.TextInput("hello"), out)
	stopOperation()
	if err != nil {
		t.Fatal(err)
	}
	if turn.(*session).owner != e || turn.(*session).process.Cmd.Process.Pid != pid {
		t.Fatal("StartTurn replaced the prepared native process")
	}
	var done proto.DonePayload
	for event := range out {
		if event.ID != "actual-run" || event.Type == proto.TypeError {
			t.Fatal("lost execution identity or owner lifetime", event.Type)
		}
		if event.Type == proto.TypeDone {
			if err := event.DecodePayload(&done); err != nil {
				t.Fatal(err)
			}
		}
	}
	if done.Metadata[proto.DoneMetaAgentSessionID] != "native-session" || done.Usage.Raw["claude_sdk_result"] == nil {
		t.Fatal("prepared execution lost ordinary output or frozen resume", done)
	}
	if _, err := os.Stat(filepath.Join(config.StateDir, "released")); err != nil {
		t.Fatal("completion preceded process release", err)
	}
}

func TestPreparationRejectsInputAndUnavailableProfilesBeforeLaunch(t *testing.T) {
	for _, name := range []string{"run", "prompt", "attachments", "subagents", "none", "functions", "mcp", "old-runtime"} {
		t.Run(name, func(t *testing.T) {
			config := preparationFixture(t, name)
			req := preparationRequest()
			switch name {
			case "run":
				req.RunID = "unexpected"
			case "prompt":
				req.Input = proto.TextInput("unexpected")
			case "attachments":
				req.Input = proto.MessageInput{{Content: []proto.InputContent{{Type: "input_image"}}}}
			case "subagents":
				req.ObserveSubagentIdentities = true
			case "none":
				req.DisableExecutionEnvironment = true
			case "functions":
				req.FunctionTools = []proto.FunctionTool{{Name: "hello", Parameters: json.RawMessage(`{"type":"object"}`)}}
			case "mcp":
				req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp"}}
			}
			if _, err := NewExecutorFactory(config)(t.Context(), req); err == nil {
				t.Fatal("invalid preparation was accepted")
			}
			if _, err := os.Stat(filepath.Join(config.StateDir, "launched")); !os.IsNotExist(err) {
				t.Fatal("rejection launched native preparation")
			}
		})
	}
}

func TestPreparationFailureAndUnusedRelease(t *testing.T) {
	for _, mode := range []string{"history-missing", "invalid-receipt", "close", "owner-cancel", "native-exit", "invalid-start", "cancelled-start"} {
		t.Run(mode, func(t *testing.T) {
			config := preparationFixture(t, mode)
			owner, stop := context.WithCancel(t.Context())
			defer stop()
			resource, err := NewExecutorFactory(config)(owner, preparationRequest())
			if mode == "history-missing" || mode == "invalid-receipt" {
				if err == nil || mode == "history-missing" && !strings.Contains(err.Error(), "history_unavailable") {
					t.Fatal("preparation failure was lost", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			e := resource.(*executor)
			defer e.Close(context.Background())
			switch mode {
			case "close":
				err = e.Close(t.Context())
			case "owner-cancel":
				stop()
			case "native-exit":
				err = e.base.process.Cmd.Process.Signal(syscall.SIGKILL)
			case "invalid-start", "cancelled-start":
				operation, cancel := context.WithCancel(t.Context())
				prompt := ""
				if mode == "cancelled-start" {
					prompt = "hello"
					cancel()
				}
				_, startErr := e.StartTurn(operation, "run", proto.TextInput(prompt), make(chan proto.Envelope, 8))
				cancel()
				if startErr == nil {
					t.Fatal("invalid or cancelled StartTurn succeeded")
				}
				err = e.Close(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-e.done:
			case <-time.After(5 * time.Second):
				t.Fatal("unused process was not settled")
			}
			if _, err := e.StartTurn(t.Context(), "late", proto.TextInput("hello"), make(chan proto.Envelope, 8)); err == nil {
				t.Fatal("released Executor was reusable")
			}
		})
	}
}
