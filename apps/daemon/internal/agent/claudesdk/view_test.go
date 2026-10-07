//go:build unix

package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/viewloader"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestViewExecutorLaunchesAClosedGatewayEnvironment(t *testing.T) {
	const realKey = "sk-ant-real-key-sentinel"
	t.Setenv("ANTHROPIC_API_KEY", realKey)
	t.Setenv("OAC_VIEW_SENTINEL", "host-environment")
	view := resolveTestView(t)
	home := t.TempDir()
	var launched clirunner.StartOptions
	requests := make(chan []byte, 1)
	session := agent.ViewSession{
		Home:  agent.ViewDir{Host: home, View: agent.ViewPrivateRoot + "/" + agent.ViewHomeName},
		Proxy: "http://127.0.0.1:17100",
		MCP:   []agent.MCPBinding{{ServerLabel: "docs", Transport: "http", ServerURL: "http://127.0.0.1:17102/mcp/docs"}},
		Launch: func(options clirunner.StartOptions) (*clirunner.Process, error) {
			launched = options
			return startViewBridge(options, requests)
		},
	}
	req := proto.PromptRequestPayload{DisableSubagents: true, LocalEnvironment: &proto.LocalEnvironment{ID: "environment", WorkspaceRoot: "/workspace", NetworkAccess: "enabled"},
		Model: "fixture", ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "http://127.0.0.1:17101", APIKey: modelprovider.Placeholder}}
	executor, err := view.Executor(t.Context(), req, session)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer executor.Close(ctx)
	request := <-requests

	if !slices.Contains(view.LocalExec, launched.Binary) || !slices.Equal(launched.Args, []string{agent.ViewPrivateRoot + "/claude-sdk/dist/main.js"}) || launched.Dir != "/workspace" {
		t.Fatalf("launch = %+v, want the closure's node running the bridge in the workspace", launched)
	}
	env := map[string]string{}
	for _, entry := range launched.Env {
		name, value, _ := strings.Cut(entry, "=")
		env[name] = value
	}
	shell := agent.ViewPrivateRoot + "/" + agent.ViewShimName + "/bash"
	for name, want := range map[string]string{
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:17101", "ANTHROPIC_API_KEY": modelprovider.Placeholder,
		"HOME": session.Home.View + "/home", "CLAUDE_CONFIG_DIR": session.Home.View + "/config", "TMPDIR": session.Home.View + "/tmp", "XDG_RUNTIME_DIR": session.Home.View + "/xdg",
		"PATH": agent.ViewPrivateRoot + "/" + agent.ViewShimName, "LD_LIBRARY_PATH": agent.ViewPrivateRoot + "/lib",
		"HTTPS_PROXY": session.Proxy, "http_proxy": session.Proxy, "NO_PROXY": "127.0.0.1,localhost",
		"CLAUDE_CODE_CERT_STORE": "bundled", "SHELL": shell, "CLAUDE_CODE_SHELL": shell, "USE_BUILTIN_RIPGREP": "0",
		"CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS": "1", "CLAUDE_CODE_TOOL_MEMORY_LIMIT": "0",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok || !strings.HasPrefix(env["CLAUDE_CODE_TMPDIR"], "/tmp/oac-claude-") {
		t.Errorf("ANTHROPIC_AUTH_TOKEN set or CLAUDE_CODE_TMPDIR %q outside the shared /tmp", env["CLAUDE_CODE_TMPDIR"])
	}
	if _, ok := env["OAC_VIEW_SENTINEL"]; ok {
		t.Error("the agent host's environment reached the Harness")
	}

	var start startRequest
	if err := json.Unmarshal(request, &start); err != nil || start.Type != "executor_prepare" || start.Cwd != "/workspace" || start.Workspace == nil ||
		start.Workspace.Home != env["HOME"] || start.Workspace.State != env["CLAUDE_CONFIG_DIR"] || len(start.Workspace.MCP) != 1 ||
		start.Workspace.MCP[0].ServerURL != "http://127.0.0.1:17102/mcp/docs" || start.Workspace.MCP[0].BearerTokenEnvVar != "" {
		t.Fatalf("bridge request = %s, %v", request, err)
	}
	for _, name := range viewHomeDirs {
		if info, err := os.Lstat(filepath.Join(home, name)); err != nil || !info.IsDir() {
			t.Errorf("home %s: %v", name, err)
		}
	}
	leaked := strings.Contains(strings.Join(append(launched.Args, launched.Env...), "\n")+string(request), realKey)
	_ = filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			raw, _ := os.ReadFile(path)
			leaked = leaked || strings.Contains(string(raw), realKey)
		}
		return nil
	})
	if leaked {
		t.Fatal("the real key reached the view")
	}

	// The Harness runs a stdio binding's alias without arguments.
	docs := session.MCP
	session.MCP = []agent.MCPBinding{{ServerLabel: "local", Transport: "stdio", Stdio: &proto.EnvironmentMCP{
		Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: agent.ViewAlias(0)}}}}
	stdio, err := view.Executor(t.Context(), req, session)
	if err != nil {
		t.Fatal(err)
	}
	defer stdio.Close(ctx)
	var stdioStart startRequest
	if err := json.Unmarshal(<-requests, &stdioStart); err != nil || stdioStart.Workspace == nil || len(stdioStart.Workspace.MCP) != 1 ||
		stdioStart.Workspace.MCP[0].Command != agent.ViewAlias(0) || stdioStart.Workspace.MCP[0].Args != nil {
		t.Fatalf("stdio MCP = %+v, %v", stdioStart.Workspace, err)
	}

	// With environment none the bridge runs without a workspace in the work directory.
	none := req
	none.LocalEnvironment, none.DisableExecutionEnvironment = nil, true
	session.MCP = docs
	noneExecutor, err := view.Executor(t.Context(), none, session)
	if err != nil {
		t.Fatal(err)
	}
	defer noneExecutor.Close(ctx)
	var noneStart startRequest
	if err := json.Unmarshal(<-requests, &noneStart); err != nil || launched.Dir != "/.oac/home/work" || noneStart.Cwd != launched.Dir || noneStart.Workspace != nil ||
		noneStart.MCPHTTPServers == nil || len(*noneStart.MCPHTTPServers) != 1 || (*noneStart.MCPHTTPServers)[0].ServerURL != "http://127.0.0.1:17102/mcp/docs" {
		t.Fatalf("environment none launched in %q with %+v, %v", launched.Dir, noneStart, err)
	}
	for _, entry := range launched.Env {
		if strings.HasPrefix(entry, "CLAUDE_CODE_TMPDIR=") || strings.HasPrefix(entry, "SHELL=") {
			t.Errorf("environment none sets the workspace tools' %s", entry)
		}
	}

	// A link the Session uid planted in its home is never followed.
	session.MCP, session.Home.Host = nil, t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(session.Home.Host, "config")); err != nil {
		t.Fatal(err)
	}
	if _, err := view.Executor(t.Context(), req, session); err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("planted config link = %v, want a failed preparation", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside the home = %v, %v, want it unchanged", entries, err)
	}
}

// resolveTestView registers the declaration with a view over a probe fixture
// and resolves it, so the Registry's gateway check runs before the factory.
func resolveTestView(t *testing.T) agent.View {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	probe := Config{Node: filepath.Join(root, "bin", "node"), Entrypoint: filepath.Join(root, "bundle", "dist", "main.js")}
	binary, err := filepath.EvalSymlinks(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		probe.Node:       "#!/bin/sh\nexport GO_CLAUDE_READINESS_HELPER=1 READINESS_MODE=view\nexec '" + strings.ReplaceAll(binary, "'", "'\\''") + "' \"$@\"\n",
		probe.Entrypoint: "", filepath.Join(root, "bundle", "dist", "runtime_check.js"): "", filepath.Join(root, "bundle", "native", "claude"): "",
		filepath.Join(root, "lib", "ld.so"): "",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil || os.WriteFile(path, []byte(content), 0o700) != nil {
			t.Fatal(path, err)
		}
	}
	lib := agent.ViewMount{Name: viewloader.MountName, HostDir: filepath.Join(root, "lib")}
	loader := viewloader.Fragment{Closure: []agent.ViewMount{lib}, Overlays: []agent.ViewOverlay{{Path: "/lib64/ld-linux-x86-64.so.2", Source: filepath.Join(root, "lib", "ld.so"), Exec: true}}, LibraryPath: lib.Path()}
	declared := declareView(probe, RuntimeInfo{NativePath: "native/claude"}, probe.Node, filepath.Join(root, "bundle"), "dist/main.js", loader)
	registry := agent.NewRegistry()
	registry.Register(Declaration, agent.Runtime{Info: Declaration.Info, View: declared})
	view, err := registry.ResolveView(Declaration.Info.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.LocalExec, agent.ViewPrivateRoot+"/claude-sdk/native/claude") {
		t.Fatalf("LocalExec = %v, want the native claude", view.LocalExec)
	}
	return view
}

// startViewBridge stands in for the bridge in a view: it records the
// preparation request, reports ready and ends when signalled.
func startViewBridge(options clirunner.StartOptions, requests chan<- []byte) (*clirunner.Process, error) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	bridge := &viewBridge{ended: make(chan struct{})}
	go func() {
		line, _ := bufio.NewReader(stdinReader).ReadBytes('\n')
		requests <- line
		_, _ = fmt.Fprintln(stdoutWriter, `{"type":"executor_ready","protocol":3}`)
		<-bridge.ended
		_ = stdoutWriter.Close()
		_ = stdinReader.Close()
	}()
	return clirunner.FromHandle(bridge, clirunner.HandleOptions{Parent: options.Parent, Stdin: stdinWriter, Stdout: stdoutReader, Stderr: io.NopCloser(strings.NewReader(""))})
}

type viewBridge struct {
	once  sync.Once
	ended chan struct{}
}

func (b *viewBridge) Signal(syscall.Signal) error { b.end(); return nil }
func (b *viewBridge) Wait() (int, error)          { <-b.ended; return 0, nil }
func (b *viewBridge) Close() error                { b.end(); return nil }
func (b *viewBridge) end()                        { b.once.Do(func() { close(b.ended) }) }
