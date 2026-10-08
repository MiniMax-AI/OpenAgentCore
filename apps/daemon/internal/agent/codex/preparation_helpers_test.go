package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

type preparationFrame struct {
	PID    int             `json:"pid"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// preparationFixture prepares a fake codex, which records each frame under
// root, in a test view whose home is root/home.
func preparationFixture(t *testing.T) (agent.PrepareRequest, sessionConfig, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("OAC_TEST_PREPARATION_FRAMES", filepath.Join(root, "frames.jsonl"))
	t.Setenv("OAC_TEST_PREPARATION_STATUS", filepath.Join(root, "environment-status"))
	t.Setenv("OAC_TEST_PREPARATION_BLOCK", "")
	binary := filepath.Join(root, "fake-codex")
	executable := "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "'"
	body := "#!/bin/sh\nOAC_TEST_PREPARATION_FAKE=1 exec " + executable + " -test.run=^TestPreparationFakeCodexProcess$ -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := testView(t, binary, filepath.Join(root, "home"))
	// A view always owns the native MCP configuration, empty without MCP.
	config := filepath.Join(root, "mcp-config.json")
	if err := os.WriteFile(config, []byte(`{"config":{"mcp_servers":{},"features":{"plugins":false,"apps":false},"mcp_oauth_credentials_store":"file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_TEST_PREPARATION_MCP_CONFIG", config)
	req := prepared(t, "prepared-session", proto.PromptRequestPayload{
		AgentKind:                   "codex",
		Model:                       "fixture-model",
		ModelProvider:               fixtureProvider(),
		ExecutionControls:           &proto.ExecutionControls{TextVerbosity: "medium"},
		DisableExecutionEnvironment: true,
		FunctionTools:               []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}}}`)}},
	})
	return req, cfg, root
}

// testView runs binary as codex in a view whose home is home, at the same
// path on the host and in the view. Launch runs binary on this host, with the
// plan's environment over the test's.
func testView(t testing.TB, binary, home string) sessionConfig {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, agent.ViewWorkName), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := defaultSessionConfig()
	cfg.codexBinary = binary
	cfg.view = &viewLaunch{binary: binary, ViewSession: agent.ViewSession{
		Home:  agent.ViewDir{Host: home, View: home},
		Proxy: "http://127.0.0.1:9",
		Launch: func(opts clirunner.StartOptions) (*clirunner.Process, error) {
			opts.Env = append(os.Environ(), opts.Env...)
			return clirunner.Start(opts)
		},
	}}
	return cfg
}

// testPlan builds req's session plan with CODEX_HOME in a new directory.
func testPlan(t testing.TB, req agent.PrepareRequest) (SessionPlan, error) {
	t.Helper()
	home := t.TempDir()
	return buildSessionPlan(req, func() (agent.ViewDir, error) { return agent.ViewDir{Host: home, View: home}, nil })
}

// prepared is req as the registry and dispatch hand it to a Codex factory.
func prepared(t testing.TB, stateKey string, req proto.PromptRequestPayload) agent.PrepareRequest {
	t.Helper()
	configuration, err := Declaration.Configuration.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	return agent.PrepareRequest{PromptRequestPayload: req, Prepared: configuration, StateKey: stateKey}
}

func fixtureProvider() *modelprovider.Provider {
	return &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://model.example/v1", APIKey: "fixture-key"}
}

func preparationFrames(t *testing.T, root string) []preparationFrame {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "frames.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var frames []preparationFrame
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var frame preparationFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func waitPreparationMethod(t *testing.T, root, method string) []preparationFrame {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		frames := preparationFrames(t, root)
		for _, frame := range frames {
			if frame.Method == method {
				return frames
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native request not observed", method)
	return nil
}

func assertPreparationOnly(t *testing.T, root string) {
	t.Helper()
	for _, frame := range preparationFrames(t, root) {
		if strings.HasPrefix(frame.Method, "thread/") || strings.HasPrefix(frame.Method, "turn/") {
			t.Fatal("preparation started native work", frame.Method)
		}
	}
}

func preparedCatalogs(t *testing.T, root string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "home", viewCodexHome, "model-catalog-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func waitExecutorRelease(t *testing.T, e *Executor, root string) {
	t.Helper()
	select {
	case <-e.base.rpc.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("prepared child was not released")
	}
	deadline := time.Now().Add(time.Second)
	for len(preparedCatalogs(t, root)) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(preparedCatalogs(t, root)) != 0 {
		t.Fatal("prepared model catalog was not cleaned")
	}
}

func TestPreparationFakeCodexProcess(t *testing.T) {
	if os.Getenv("OAC_TEST_PREPARATION_FAKE") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "models" {
			_, _ = os.Stdout.WriteString(`{"models":[{"slug":"fixture-model","support_verbosity":true}]}`)
			os.Exit(0)
		}
	}
	log, err := os.OpenFile(os.Getenv("OAC_TEST_PREPARATION_FRAMES"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(2)
	}
	frames := json.NewEncoder(log)
	output := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	turnNumber := 0
	currentTurn := ""
	executorMode := os.Getenv("OAC_TEST_EXECUTOR_MODE")
	var skills SkillsExtraRootsSetParams
	for scanner.Scan() {
		var frame preparationFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			os.Exit(3)
		}
		frame.PID = os.Getpid()
		if frames.Encode(frame) != nil {
			os.Exit(4)
		}
		var result any = map[string]any{}
		switch frame.Method {
		case "initialize":
			result = map[string]string{"userAgent": "fixture-codex"}
		case "environment/status":
			if os.Getenv("OAC_TEST_PREPARATION_BLOCK") == "1" {
				for {
					time.Sleep(time.Second)
				}
			}
			status := "unknown"
			if data, err := os.ReadFile(os.Getenv("OAC_TEST_PREPARATION_STATUS")); err == nil {
				status = string(data)
			}
			result = map[string]string{"status": status}
		case "skills/extraRoots/set":
			if json.Unmarshal(frame.Params, &skills) != nil {
				os.Exit(7)
			}
		case "skills/list":
			// Each registered root holds one Skill.
			listed := []map[string]string{}
			for _, root := range skills.ExtraRoots {
				listed = append(listed, map[string]string{"name": filepath.Base(root), "path": filepath.Join(root, "SKILL.md")})
			}
			result = map[string]any{"data": []any{map[string]any{"skills": listed, "errors": []any{}}}}
		case "config/read":
			if delay := os.Getenv("OAC_TEST_PREPARATION_MCP_DELAY"); delay != "" {
				duration, err := time.ParseDuration(delay)
				if err != nil {
					os.Exit(6)
				}
				time.Sleep(duration)
			}
			data, err := os.ReadFile(os.Getenv("OAC_TEST_PREPARATION_MCP_CONFIG"))
			if err != nil || json.Unmarshal(data, &result) != nil {
				os.Exit(6)
			}
		case "thread/start", "thread/resume":
			if gate := os.Getenv("OAC_TEST_PREPARATION_THREAD_GATE"); gate != "" {
				var state []byte
				for string(state) != "ready" && string(state) != "failed" {
					state, _ = os.ReadFile(gate)
					time.Sleep(time.Millisecond)
				}
				if string(state) == "failed" {
					_ = output.Encode(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "error": map[string]any{"code": -32603, "message": "required MCP initialization failed"}})
					continue
				}
			}
			result = map[string]any{"thread": map[string]string{"id": "fixture-native-thread"}, "model": "fixture-model"}
		case "thread/backgroundTerminals/list":
			result = map[string]any{"data": []any{}}
			if executorMode == "terminal-cleanup" {
				if _, err := os.Stat(os.Getenv("OAC_TEST_PREPARATION_FRAMES") + ".terminated"); os.IsNotExist(err) {
					result = map[string]any{"data": []any{map[string]string{"processId": "owned-terminal"}}}
				}
			}
		case "thread/backgroundTerminals/terminate":
			var target struct {
				ThreadID  string `json:"threadId"`
				ProcessID string `json:"processId"`
			}
			if json.Unmarshal(frame.Params, &target) != nil || target.ThreadID != "fixture-native-thread" || target.ProcessID != "owned-terminal" {
				os.Exit(8)
			}
			_, allowed := os.Stat(os.Getenv("OAC_TEST_PREPARATION_FRAMES") + ".allow-cleanup")
			result = map[string]any{"terminated": allowed == nil}
			if allowed == nil {
				_ = os.WriteFile(os.Getenv("OAC_TEST_PREPARATION_FRAMES")+".terminated", nil, 0600)
			}
		case "turn/steer":
			result = map[string]any{"turnId": currentTurn}
		case "turn/interrupt":
			if executorMode == "interrupt-error" {
				_ = output.Encode(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32603, "message": "interrupt rejected"}})
				continue
			}
		case "turn/start":
			if delay := os.Getenv("OAC_TEST_EXECUTOR_START_DELAY"); delay != "" {
				duration, err := time.ParseDuration(delay)
				if err != nil {
					os.Exit(6)
				}
				time.Sleep(duration)
			}
			if executorMode != "" {
				turnNumber++
				currentTurn = fmt.Sprintf("fixture-turn-%d", turnNumber)
				if turnNumber > 1 {
					_ = output.Encode(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": "fixture-native-thread", "turn": map[string]string{"id": fmt.Sprintf("fixture-turn-%d", turnNumber-1)}}})
				}
				if executorMode == "disconnect" {
					os.Exit(0)
				}
				if executorMode == "start-error" {
					_ = output.Encode(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32603, "message": "start rejected"}})
					continue
				}
				result = map[string]any{"turn": map[string]string{"id": currentTurn}}
				break
			}
			result = map[string]any{"turn": map[string]string{"id": "fixture-native-turn"}}
		}
		if output.Encode(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "result": result}) != nil {
			os.Exit(5)
		}

		if executorMode != "" && (frame.Method == "turn/start" || frame.Method == "turn/interrupt") {
			if frame.Method == "turn/interrupt" && executorMode == "interrupt-no-terminal" {
				continue
			}
			held := strings.Contains(string(frame.Params), "hold")
			if frame.Method == "turn/start" {
				_ = output.Encode(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": "fixture-native-thread", "turn": map[string]string{"id": currentTurn}}})
				if held {
					_ = output.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": "fixture-native-thread", "turnId": currentTurn, "itemId": "held-message", "delta": "holding"}})
				}
			}
			if frame.Method == "turn/interrupt" || !held {
				status := "completed"
				if frame.Method == "turn/interrupt" {
					status = "interrupted"
				}
				_ = output.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "fixture-native-thread", "turn": map[string]string{"id": currentTurn, "status": status}}})
			}
			continue
		}
		if frame.Method == "turn/start" {
			_ = output.Encode(map[string]any{"jsonrpc": "2.0", "method": "turn/started", "params": map[string]any{"threadId": "fixture-native-thread", "turn": map[string]string{"id": "fixture-native-turn"}}})
		}
	}
	_ = log.Close()
	os.Exit(0)
}
