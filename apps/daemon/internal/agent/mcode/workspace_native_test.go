package mcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// This opt-in test makes real model calls with the native tools in a workspace.
// It needs the built companion (OAC_RUNTIME_MCODE_COMPANION, OAC_RUNTIME_MCODE_NODE), the native CLI
// (OAC_RUNTIME_MCODE_BIN) and private provider options (OAC_TEST_MCODE_REAL_OPTIONS).
func TestNativeMCodeWorkspaceTools(t *testing.T) {
	binary, node, companion, options := os.Getenv("OAC_RUNTIME_MCODE_BIN"), os.Getenv("OAC_RUNTIME_MCODE_NODE"), os.Getenv("OAC_RUNTIME_MCODE_COMPANION"), os.Getenv("OAC_TEST_MCODE_REAL_OPTIONS")
	if binary == "" || node == "" || companion == "" || options == "" {
		t.Skip("native executable, companion and private provider options required")
	}
	raw, err := os.ReadFile(options)
	if err != nil {
		t.Fatal("private provider options unavailable")
	}
	type result struct {
		workspace, content string
		calls              map[string][]proto.ToolCallPayload
		events             map[string]int
	}
	// run executes one Turn; a nil config selects the none placement.
	run := func(t *testing.T, c *WorkspaceConfig, search *proto.WebSearchControl, prompt string) result {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
		defer cancel()
		workspace := t.TempDir()
		if c != nil {
			c.Directory = workspace
		}
		req := testRequest(t)
		req.RunID, req.Input = "", nil
		if json.Unmarshal(raw, &req.AgentOptions) != nil {
			t.Fatal("invalid private provider options")
		}
		limit := 2
		req.DisableSubagents, req.MaxConcurrentSubagents = false, &limit
		if c != nil {
			req.DisableExecutionEnvironment = false
			req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", WorkspaceRoot: workspace}
		}
		if search != nil {
			req.ExecutionControls = &proto.ExecutionControls{WebSearch: search}
		}
		if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("alpha\n"), 0600); err != nil {
			t.Fatal(err)
		}
		skill := filepath.Join(workspace, ".claude", "skills", "probe-phrase")
		if err := os.MkdirAll(skill, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: probe-phrase\ndescription: Gives the probe phrase.\n---\nThe probe phrase is violet-anchor-58.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		value, err := NewExecutorFactory(testStateRoot(t), c)(ctx, req)
		if err != nil {
			t.Fatal("native Executor preparation failed:", err)
		}
		e := value.(*executor)
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			if e.Close(cleanup) != nil {
				t.Error("native owner cleanup failed")
			}
		}()
		out := make(chan proto.Envelope, 1024)
		turn, err := e.StartTurn(ctx, "turn", proto.TextInput(prompt), out)
		if err != nil {
			t.Fatal(err)
		}
		r := result{workspace: workspace, calls: map[string][]proto.ToolCallPayload{}, events: map[string]int{}}
		for event := range out {
			r.events[event.Type]++
			switch event.Type {
			case proto.TypeError:
				t.Fatalf("native Turn reported an error: %s", event.Payload)
			case proto.TypeToolCall:
				var call proto.ToolCallPayload
				if json.Unmarshal(event.Payload, &call) != nil {
					t.Fatal("invalid tool call")
				}
				r.calls[call.ID] = append(r.calls[call.ID], call)
			case proto.TypeDone:
				var done proto.DonePayload
				_ = json.Unmarshal(event.Payload, &done)
				r.content = done.Content
			}
		}
		if _, err := turn.AwaitSettlement(ctx); err != nil {
			t.Fatal(err)
		}
		return r
	}
	workspace := &WorkspaceConfig{Binary: binary, Node: node, Companion: companion}
	// finished returns the final observation of each command call, keyed by command text.
	finished := func(calls map[string][]proto.ToolCallPayload) map[string]*proto.ToolObservation {
		result := map[string]*proto.ToolObservation{}
		for _, stages := range calls {
			if last := stages[len(stages)-1]; last.Stage == "after" && last.Observation != nil && last.Observation.Kind == "command" {
				result[last.Observation.Command] = last.Observation
			}
		}
		return result
	}
	t.Run("files and commands", func(t *testing.T) {
		r := run(t, workspace, nil, "Use your tools. 1) Read notes.txt. 2) Edit notes.txt, replacing alpha with beta. 3) Run exactly this bash command: echo bash-ok > out.txt. 4) Run exactly this bash command: sh -c 'exit 3'. Then reply DONE.")
		notes, _ := os.ReadFile(filepath.Join(r.workspace, "notes.txt"))
		out, _ := os.ReadFile(filepath.Join(r.workspace, "out.txt"))
		if strings.TrimSpace(string(notes)) != "beta" || strings.TrimSpace(string(out)) != "bash-ok" {
			t.Fatalf("workspace not changed by native tools: %q %q", notes, out)
		}
		commands := finished(r.calls)
		write, failed := commands["echo bash-ok > out.txt"], commands["sh -c 'exit 3'"]
		if write == nil || write.Status != "completed" || write.ExitCode == nil || *write.ExitCode != 0 || write.Cwd == nil || *write.Cwd != r.workspace {
			t.Fatalf("command observation: %+v", write)
		}
		if failed == nil || failed.Status != "failed" || failed.ExitCode != nil || failed.Output == nil {
			t.Fatalf("failed command observation: %+v", failed)
		}
	})
	t.Run("bash stays in the foreground", func(t *testing.T) {
		r := run(t, workspace, nil, "Use bash. 1) Run exactly this command and wait for it: sleep 20; echo slow-done. 2) Call bash with run_in_background set to true and the command: touch bg-ran. Report both tool replies.")
		slow := finished(r.calls)["sleep 20; echo slow-done"]
		if slow == nil || slow.Status != "completed" || !strings.Contains(string(slow.Output), "slow-done") {
			t.Fatalf("slow command was not completed in the foreground: %+v", slow)
		}
		background := false
		for _, stages := range r.calls {
			if stages[0].Args["run_in_background"] == true {
				background = true
				if last := stages[len(stages)-1]; last.Observation == nil || last.Observation.Status != "failed" {
					t.Fatalf("background bash was not refused: %+v", last.Observation)
				}
			}
		}
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(filepath.Join(r.workspace, "bg-ran")); err == nil || !background {
			t.Fatal("background bash ran or was never requested")
		}
	})
	t.Run("Subagents use the native tools", func(t *testing.T) {
		r := run(t, workspace, nil, "Use the task tool to start one worker Subagent with this instruction: run the bash command `echo child-ok > child.txt`, then reply done. Wait for its result, then reply DONE.")
		child, _ := os.ReadFile(filepath.Join(r.workspace, "child.txt"))
		if strings.TrimSpace(string(child)) != "child-ok" || r.events[proto.TypeSubagentIdentity] == 0 || r.events[proto.TypeSubagentItem] == 0 {
			t.Fatalf("Subagent did not run in the workspace: %q %v", child, r.events)
		}
	})
	t.Run("workspace Skills load natively", func(t *testing.T) {
		r := run(t, workspace, nil, "Use the probe-phrase Skill, then reply with the probe phrase only.")
		if !strings.Contains(r.content, "violet-anchor-58") {
			t.Fatalf("workspace Skill was not loaded: %q", r.content)
		}
	})
	t.Run("web search control", func(t *testing.T) {
		const prompt = "Without calling any tool, list the exact names of every tool you can call, comma separated."
		tools := run(t, workspace, nil, prompt).content
		disabled := run(t, workspace, &proto.WebSearchControl{Mode: "disabled", ContextSize: "medium"}, prompt).content
		if !strings.Contains(tools, "web_fetch") || !strings.Contains(tools, "bash") || strings.Contains(disabled, "web_fetch") || !strings.Contains(disabled, "bash") {
			t.Fatalf("tool inventories: %q / %q", tools, disabled)
		}
		for _, name := range []string{"website_deploy", "ask_user", "mavis", "web_search"} {
			if strings.Contains(tools, name) {
				t.Fatalf("%s is available: %q", name, tools)
			}
		}
	})
	t.Run("no Environment has no builtin tools", func(t *testing.T) {
		tools := run(t, nil, nil, "Without calling any tool, list the exact names of every tool you can call, comma separated.").content
		for _, name := range []string{"bash", "read", "write", "edit", "web_fetch"} {
			if strings.Contains(tools, name) {
				t.Fatalf("%s is available: %q", name, tools)
			}
		}
	})
}
