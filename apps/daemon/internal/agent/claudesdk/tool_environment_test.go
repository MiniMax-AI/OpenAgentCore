package claudesdk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestSelfHostedToolEnvironment(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.NetworkAccess = "enabled"
	file := filepath.Join(t.TempDir(), "tool-env.json")
	if err := os.WriteFile(file, []byte(`{"USER_VALUE":"ready"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", file)
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{NetworkAccess: "enabled", WorkspaceRoot: config.Workspace.Directory}
	profile, _, err := prepareWorkspace(config, req)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ToolEnv["USER_VALUE"] != "ready" {
		t.Fatal("self-hosted tool configuration was not applied")
	}
	if err := os.WriteFile(file, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareWorkspace(config, req); err == nil {
		t.Fatal("invalid explicit tool configuration was ignored")
	}
}
