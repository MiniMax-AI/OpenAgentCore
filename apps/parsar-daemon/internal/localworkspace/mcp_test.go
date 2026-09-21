package localworkspace

import (
	"slices"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
)

func TestEnvironmentMCPCredentialsNeverFallBackToNativeEnv(t *testing.T) {
	t.Setenv("PLUGIN_TOKEN", "native-private-value")
	installed := []agentcapabilities.InstalledMCP{{PackageRoot: "plugins/0", Server: agentplugin.MCPServer{
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

func TestMCPStdioLauncherContainsOnlyInstalledIdentity(t *testing.T) {
	server := proto.EnvironmentMCP{PackageRoot: "plugins/0", Server: agentplugin.MCPServer{
		Name: "package_tool", Type: "stdio", Command: "untrusted-command", Args: []string{"private-argument"},
	}}
	command, args := MCPStdioCommand(server)
	if command != "/usr/bin/python3" || !slices.Equal(args, []string{"-I", "-S", MCPInitializer, "stdio", "plugins/0", "package_tool"}) {
		t.Fatal("native configuration included untrusted process configuration")
	}
}
