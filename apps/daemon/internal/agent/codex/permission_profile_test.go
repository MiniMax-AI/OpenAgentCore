package codex

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeUsesHostPermissions(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	{
		req := proto.PromptRequestPayload{AgentStateKey: "session", DisableSubagents: true, LocalEnvironment: &proto.LocalEnvironment{WorkspaceRoot: t.TempDir()}}
		plan, _, err := prepareSessionPlan(t.Context(), req, sessionConfig{stateRoot: testStateRoot(t)})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Cwd != req.LocalEnvironment.WorkspaceRoot {
			t.Fatal("native cwd is not the bound workspace root", plan.Cwd)
		}
		for _, kv := range plan.ExtraConfig {
			if kv[0] == "default_permissions" {
				t.Fatal("obsolete permission wrapper", kv)
			}
		}
		plan.Cleanup()
	}
}

func TestSelfHostedToolEnvironmentCannotRedirectNativeHistory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := filepath.Join(root, "tool-env.json")
	if err := os.WriteFile(config, []byte(`{"CODEX_HOME":"wrong","HOME":"wrong","USERPROFILE":"wrong","USER_VALUE":"ready"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", config)
	req := proto.PromptRequestPayload{AgentStateKey: "session", DisableSubagents: true, LocalEnvironment: &proto.LocalEnvironment{}}
	plan, _, err := prepareSessionPlan(t.Context(), req, sessionConfig{stateRoot: testStateRoot(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	values := map[string]string{}
	for _, entry := range plan.Env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if values["CODEX_HOME"] == "" || values["CODEX_HOME"] == "wrong" || values["HOME"] == "wrong" || values["USERPROFILE"] == "wrong" || values["USER_VALUE"] != "ready" {
		t.Fatal("initialization changed native state identity")
	}
}
