package localworkspace

import (
	"os"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestEnvironmentMCPCredentialsNeverFallBackToNativeEnv(t *testing.T) {
	t.Setenv("PLUGIN_TOKEN", "native-private-value")
	installed := []agentcapabilities.InstalledMCP{{PackageRoot: "plugins/0", Server: proto.PluginMCPServer{
		Name: "remote", Type: "http", URL: "https://example.com/mcp", BearerTokenEnvVar: "PLUGIN_TOKEN",
	}}}
	if _, err := resolveEnvironmentMCP(installed, nil); err == nil {
		t.Fatal("native credential became a user Plugin credential")
	}
	servers, err := resolveEnvironmentMCP(installed, map[string]string{"PLUGIN_TOKEN": "user-token"})
	if err != nil || len(servers) != 1 || servers[0].BearerToken == nil || *servers[0].BearerToken != "user-token" {
		t.Fatal("initialized credential was not selected", err)
	}
	installed = append(installed, agentcapabilities.InstalledMCP{PackageRoot: "plugins/1", Server: installed[0].Server})
	if _, err := resolveEnvironmentMCP(installed, map[string]string{"PLUGIN_TOKEN": "user-token"}); err == nil {
		t.Fatal("ambiguous server identity accepted")
	}
}

func TestDeclaredMCPVariablesNeverFallBackToNativeEnv(t *testing.T) {
	t.Setenv("SERVER_TOKEN", "native-private-value")
	servers := []proto.MCPHTTPServer{{ServerLabel: "local", Stdio: &proto.MCPStdio{Command: "run", Cwd: "/", EnvVars: []string{"SERVER_TOKEN"}}}}
	if requireDeclaredVariables(servers, nil) == nil || requireDeclaredVariables(servers, map[string]string{"SERVER_TOKEN": ""}) != nil {
		t.Fatal("env_vars did not resolve only from the initialized env")
	}
}

func TestMCPStdioLauncherContainsOnlyInstalledIdentity(t *testing.T) {
	server := proto.EnvironmentMCP{InstallationRoot: "/private/runtime/capabilities", PackageRoot: "plugins/0", Server: proto.PluginMCPServer{
		Name: "package_tool", Type: "stdio", Command: "untrusted-command", Args: []string{"private-argument"},
	}}
	command, args := MCPStdioCommand(server)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if command != executable || !slices.Equal(args, []string{"runtime-mcp-exec", "/private/runtime/capabilities", "plugins/0", "package_tool"}) {
		t.Fatal("native configuration included untrusted process configuration")
	}
}
