package cli

import (
	"slices"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
)

func TestRuntimeMCPSelectsOnlyInstalledUserEnvironment(t *testing.T) {
	manifest := agentcapabilities.Manifest{MCP: []agentcapabilities.InstalledMCP{{PackageRoot: "plugins/0", Server: agentplugin.MCPServer{
		Name: "local", Type: "stdio", Command: "python3", Args: []string{"proof.py", "${LITERAL}"}, EnvVars: []string{"SELECTED"}, CWD: "./server",
	}}}}
	t.Setenv("SELECTED", "private-native-value")
	t.Setenv("NATIVE_ONLY", "private-native-value")
	got, err := resolveMCPInvocation(manifest, "plugins/0", "local", map[string]string{"SELECTED": "user-value", "UNDECLARED": "not-injected"})
	if err != nil || got.command != "python3" || got.cwd != agentcapabilities.Directory+"/plugins/0/server" ||
		!slices.Equal(got.args, []string{"python3", "proof.py", "${LITERAL}"}) || !slices.Contains(got.env, "SELECTED=user-value") ||
		slices.Contains(got.env, "UNDECLARED=not-injected") || slices.Contains(got.env, "NATIVE_ONLY=private-native-value") {
		t.Fatalf("incorrect isolated invocation: %+v %v", got, err)
	}
	if _, err := resolveMCPInvocation(manifest, "plugins/0", "local", map[string]string{}); err == nil {
		t.Fatal("missing user value fell back to native environment")
	}
	if _, err := resolveMCPInvocation(manifest, "plugins/1", "local", map[string]string{"SELECTED": "x"}); err == nil {
		t.Fatal("undeclared package selected")
	}
}

func TestRuntimeMCPKeepsAbsoluteCWDAndRejectsHTTP(t *testing.T) {
	manifest := agentcapabilities.Manifest{MCP: []agentcapabilities.InstalledMCP{{PackageRoot: "plugins/0", Server: agentplugin.MCPServer{
		Name: "local", Type: "stdio", Command: "python3", CWD: "/workspace",
	}}}}
	got, err := resolveMCPInvocation(manifest, "plugins/0", "local", nil)
	if err != nil || got.cwd != "/workspace" {
		t.Fatalf("absolute cwd changed: %+v %v", got, err)
	}
	manifest.MCP[0].Server.Type = "http"
	if _, err := resolveMCPInvocation(manifest, "plugins/0", "local", nil); err == nil {
		t.Fatal("HTTP configuration treated as a process")
	}
}
