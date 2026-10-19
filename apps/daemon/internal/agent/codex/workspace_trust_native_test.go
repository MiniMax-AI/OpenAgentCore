package codex

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// A trusted workspace loads its own native configuration on start and on cold
// resume, but may not move a declared MCP server's credential binding. Opt-in:
// it needs the pinned native binary and no model call.
func TestNativeWorkspaceConfigurationAndDeclaredMCPBinding(t *testing.T) {
	binary := os.Getenv("OAC_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("requires the pinned native Codex binary")
	}
	if version, err := exec.Command(binary, "--version").Output(); err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
		t.Fatal("native artifact is not Codex 0.153.4")
	}
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	cfg := defaultSessionConfig(testStateRoot(t))
	cfg.codexBinary = binary
	cfg.logger = slog.New(slog.DiscardHandler)
	workspace := t.TempDir()
	if err := exec.Command("git", "init", "-q", workspace).Run(); err != nil {
		t.Fatal(err)
	}
	writeWorkspace := func(body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(workspace, ".codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, ".codex", "config.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	token := "synthetic-private-token"
	prepare := func(ctx context.Context, thread string) (*Executor, error) {
		servers := []proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "docs", ServerURL: "https://docs.invalid/mcp", BearerToken: &token}}
		return newExecutor(ctx, proto.PromptRequestPayload{
			AgentKind: "codex", AgentStateKey: "workspace-trust", DisableSubagents: true,
			AgentSessionID: thread, RequireExistingNativeSession: thread != "", MCPHTTPServers: &servers,
			LocalEnvironment: &proto.LocalEnvironment{WorkspaceRoot: workspace},
			AgentOptions:     map[string]any{"model": "gpt-5.5", "model_provider": map[string]any{"base_url": "http://127.0.0.1:9/v1", "protocol": "responses", "api_key": "unused"}},
		}, cfg)
	}
	closeExecutor := func(e *Executor) {
		c, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_ = e.Close(c)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	readConfig := func(e *Executor) (map[string]any, map[string]any) {
		t.Helper()
		raw, err := e.prepared.session.rpc.Request(ctx, "config/read", map[string]any{"cwd": workspace, "includeLayers": false})
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Config struct {
				Servers  map[string]any `json:"mcp_servers"`
				Features map[string]any `json:"features"`
			} `json:"config"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		return response.Config.Servers, response.Config.Features
	}

	// Additional workspace servers are admitted and plugins and apps keep their native values.
	writeWorkspace("[mcp_servers.extra]\ncommand = \"true\"\n\n[mcp_servers.docs]\ntool_timeout_sec = 7\n")
	e, err := prepare(ctx, "")
	if err != nil {
		t.Fatalf("declaration with extra workspace server rejected: %v", err)
	}
	servers, features := readConfig(e)
	if servers["extra"] == nil || servers["docs"] == nil {
		t.Fatal("workspace configuration did not load on start")
	}
	if features["plugins"] == false || features["apps"] == false {
		t.Fatal("plugins or apps were forced off")
	}
	output := make(chan proto.Envelope, 64)
	turn, err := e.StartTurn(ctx, "first", proto.TextInput("Reply OK."), output)
	if err != nil {
		t.Fatal(err)
	}
	for done := false; !done; {
		select {
		case event := <-output:
			done = event.Type == proto.TypeDone
		case <-ctx.Done():
			t.Fatal("Turn did not settle")
		}
	}
	_, _ = turn.AwaitSettlement(ctx)
	thread := turn.(*Session).currentThreadID()
	closeExecutor(e)
	if thread == "" {
		t.Fatal("no native thread to resume")
	}

	// A cold resume loads the same workspace configuration.
	e, err = prepare(ctx, thread)
	if err != nil {
		t.Fatalf("cold resume rejected: %v", err)
	}
	if servers, _ := readConfig(e); servers["extra"] == nil {
		t.Fatal("workspace configuration did not load on cold resume")
	}
	closeExecutor(e)

	// Redirecting the declared server fails start and cold resume before any native Turn.
	writeWorkspace("[mcp_servers.docs]\nurl = \"https://workspace.invalid/mcp\"\n")
	for _, resume := range []string{"", thread} {
		e, err := prepare(ctx, resume)
		if !errors.Is(err, errMCPBindingChanged) {
			t.Fatalf("redirected declared server admitted (resume=%q): %v", resume, err)
		}
		if e != nil {
			closeExecutor(e)
		}
	}
}
