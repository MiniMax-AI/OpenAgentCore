package claudecode_test

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
)

func TestBuildArgsBaseHasStreamFlags(t *testing.T) {
	res, err := claudecode.BuildArgs(nil, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()

	wantContains := [][2]string{
		{"--output-format", "stream-json"},
		{"--input-format", "stream-json"},
		{"--permission-prompt-tool", "stdio"},
	}
	for _, w := range wantContains {
		if !containsPair(res.Args, w[0], w[1]) {
			t.Errorf("missing flag pair %s=%s in %v", w[0], w[1], res.Args)
		}
	}
	if !slices.Contains(res.Args, "--verbose") {
		t.Errorf("missing --verbose in %v", res.Args)
	}
	if !slices.Contains(res.Args, "--include-partial-messages") {
		t.Errorf("missing --include-partial-messages in %v", res.Args)
	}
}

// IS_SANDBOX=1 must be in every env passthrough. Without it Claude
// Code 2.1.x's root-guard kills the subprocess before the first user
// message.
func TestBuildArgsAlwaysExportsIsSandbox(t *testing.T) {
	res, err := claudecode.BuildArgs(nil, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !slices.Contains(res.Env, "IS_SANDBOX=1") {
		t.Errorf("IS_SANDBOX=1 missing from env, got %v", res.Env)
	}
}

// CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1 strips opt-in beta fields
// (e.g. context_management.clear_thinking_*) from /v1/messages bodies;
// internal Anthropic-compatible gateways reject unknown fields with 400.
func TestBuildArgsAlwaysDisablesExperimentalBetas(t *testing.T) {
	res, err := claudecode.BuildArgs(nil, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !slices.Contains(res.Env, "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1") {
		t.Errorf("CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1 missing from env, got %v", res.Env)
	}
}

func TestBuildArgsHonoursPrimaryFlags(t *testing.T) {
	res, err := claudecode.BuildArgs(map[string]any{
		"model":         "sonnet",
		"mode":          "acceptEdits",
		"allowed_tools": []any{"Bash", "Read", "Write"},
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--model", "sonnet") {
		t.Errorf("--model sonnet not in %v", res.Args)
	}
	if !containsPair(res.Args, "--permission-mode", "acceptEdits") {
		t.Errorf("--permission-mode acceptEdits not in %v", res.Args)
	}
	if !containsPair(res.Args, "--allowedTools", "Bash,Read,Write") {
		t.Errorf("--allowedTools join not in %v", res.Args)
	}
}

func TestBuildArgsResumeUsesExplicitSessionID(t *testing.T) {
	res, err := claudecode.BuildArgs(map[string]any{
		"resume_session_id": "from-map",
	}, "from-arg")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--resume", "from-arg") {
		t.Errorf("explicit resume id not preferred, args=%v", res.Args)
	}
	if slices.Contains(res.Args, "from-map") {
		t.Errorf("map key leaked into args=%v", res.Args)
	}
}

func TestBuildArgsIgnoresResumeSessionIDOption(t *testing.T) {
	res, _ := claudecode.BuildArgs(map[string]any{"resume_session_id": "session_xyz"}, "")
	defer res.Cleanup()
	if slices.Contains(res.Args, "--resume") || slices.Contains(res.Args, "session_xyz") {
		t.Errorf("resume_session_id option must be ignored, args=%v", res.Args)
	}
}

func TestBuildArgsAppendSystemPromptAlone(t *testing.T) {
	res, _ := claudecode.BuildArgs(map[string]any{"system_prompt": "be terse"}, "")
	defer res.Cleanup()
	if !containsPair(res.Args, "--append-system-prompt", "be terse") {
		t.Errorf("--append-system-prompt missing, args=%v", res.Args)
	}
}

func TestBuildArgsBypassPermissionsAddsAllowDangerouslyFlag(t *testing.T) {
	// Sandbox containers run as root; without
	// --allow-dangerously-skip-permissions Claude Code refuses to
	// bypass permissions for root callers.
	res, err := claudecode.BuildArgs(map[string]any{
		"mode": "bypassPermissions",
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--permission-mode", "bypassPermissions") {
		t.Errorf("--permission-mode bypassPermissions not in %v", res.Args)
	}
	if !slices.Contains(res.Args, "--allow-dangerously-skip-permissions") {
		t.Errorf("--allow-dangerously-skip-permissions missing for bypass mode, args=%v", res.Args)
	}
}

func TestBuildArgsNonBypassModeOmitsAllowDangerouslyFlag(t *testing.T) {
	for _, mode := range []string{"acceptEdits", "default", "plan"} {
		res, err := claudecode.BuildArgs(map[string]any{"mode": mode}, "")
		if err != nil {
			t.Fatalf("BuildArgs(mode=%s): %v", mode, err)
		}
		defer res.Cleanup()
		if slices.Contains(res.Args, "--allow-dangerously-skip-permissions") {
			t.Errorf("mode=%s should not enable allow-dangerously flag, args=%v", mode, res.Args)
		}
	}
}

func TestBuildArgsOverrideSystemPromptStripAppend(t *testing.T) {
	res, _ := claudecode.BuildArgs(map[string]any{
		"system_prompt":          "be terse",
		"override_system_prompt": "you are pirate",
	}, "")
	defer res.Cleanup()
	if slices.Contains(res.Args, "--append-system-prompt") {
		t.Errorf("override should have stripped append, args=%v", res.Args)
	}
	if !containsPair(res.Args, "--system-prompt", "you are pirate") {
		t.Errorf("--system-prompt missing, args=%v", res.Args)
	}
}

func TestBuildArgsPluginDirsRepeated(t *testing.T) {
	res, _ := claudecode.BuildArgs(map[string]any{
		"plugin_dirs": []any{"/a", "/b", "/c"},
	}, "")
	defer res.Cleanup()
	got := 0
	for i, a := range res.Args {
		if a == "--plugin-dir" {
			got++
			if i+1 >= len(res.Args) {
				t.Fatalf("trailing --plugin-dir without value: %v", res.Args)
			}
		}
	}
	if got != 3 {
		t.Errorf("expected 3 --plugin-dir flags, got %d in %v", got, res.Args)
	}
}

func TestBuildArgsMCPServersWritesTempfile(t *testing.T) {
	res, err := claudecode.BuildArgs(map[string]any{
		"mcp_servers": map[string]any{
			"github": map[string]any{"command": "/usr/local/bin/mcp-github"},
		},
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()

	// Find the --mcp-config path
	path := ""
	for i, a := range res.Args {
		if a == "--mcp-config" {
			if i+1 < len(res.Args) {
				path = res.Args[i+1]
			}
		}
	}
	if path == "" {
		t.Fatalf("--mcp-config path missing, args=%v", res.Args)
	}
	if !strings.Contains(path, "parsar-daemon-mcp-") {
		t.Errorf("mcp tempfile name unexpected: %q", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat mcp tempfile: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mcp tempfile perm = %o, want 0600", perm)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mcp tempfile: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("mcp tempfile not valid json: %v", err)
	}
	if _, ok := parsed["mcpServers"]; !ok {
		t.Errorf("mcp tempfile missing mcpServers wrapper: %s", body)
	}

	// Cleanup should remove the file.
	res.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("mcp tempfile still exists after Cleanup: stat err=%v", err)
	}
}

func TestBuildArgsEnvPassthroughIsSorted(t *testing.T) {
	res, _ := claudecode.BuildArgs(map[string]any{
		"env": map[string]any{
			"ZED":           "1",
			"ANTHROPIC_KEY": "secret",
			"OTHER_FLAG":    "x",
		},
	}, "")
	defer res.Cleanup()
	want := []string{"ANTHROPIC_KEY=secret", "OTHER_FLAG=x", "ZED=1"}
	// res.Env always starts with the four baseline values; pop them off
	// before checking the sort order of the user-supplied tail.
	tail := res.Env[4:]
	if !slices.Equal(tail, want) {
		t.Errorf("env tail = %v, want sorted %v", tail, want)
	}
}

func TestBuildArgsRejectsWrongShapes(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
	}{
		{"model not string", map[string]any{"model": 7}},
		{"mode not string", map[string]any{"mode": true}},
		{"allowed_tools not array", map[string]any{"allowed_tools": "Bash"}},
		{"plugin_dirs element not string", map[string]any{"plugin_dirs": []any{"/a", 7}}},
		{"mcp_servers not object", map[string]any{"mcp_servers": "json string"}},
		{"env value not string", map[string]any{"env": map[string]any{"K": 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := claudecode.BuildArgs(tc.opts, "")
			if err == nil {
				t.Fatal("BuildArgs accepted bad shape")
			}
		})
	}
}

func containsPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
