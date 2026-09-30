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
		req := proto.PromptRequestPayload{AgentStateKey: "session", DisableSubagents: true, LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "enabled", WorkspaceRoot: t.TempDir()}}
		plan, _, err := prepareSessionPlan(t.Context(), req, sessionConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Sandbox != "danger-full-access" || plan.Permissions != "" || plan.ApprovalPolicy.String != "never" {
			t.Fatal("Runtime must bypass inner sandbox", plan)
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
		for _, network := range []string{"disabled", "restricted"} {
			req.LocalEnvironment.NetworkAccess = network
			if _, _, err := prepareSessionPlan(t.Context(), req, sessionConfig{}); err == nil {
				t.Fatal("unsupported network admitted")
			}
		}
	}
}

func TestSelfHostedToolEnvironmentCannotRedirectNativeHistory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "tool-env.json")
	if err := os.WriteFile(config, []byte(`{"CODEX_HOME":"wrong","HOME":"wrong","USERPROFILE":"wrong","USER_VALUE":"ready"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"OAC_RUNTIME_ENVIRONMENT_ID": "b3d154b8-543b-4248-97b1-665f9f418d52", "OAC_RUNTIME_SESSION_ID": "33e02e0d-6fc8-4904-9d7a-4b61b9094ae0", "OAC_RUNTIME_WORKSPACE": workspace, "OAC_RUNTIME_CAPABILITY_DIRECTORY": filepath.Join(root, "capabilities"), "OAC_RUNTIME_NETWORK_ACCESS": "enabled", "OAC_RUNTIME_TOOL_ENV_FILE": config} {
		t.Setenv(key, value)
	}
	req := proto.PromptRequestPayload{AgentStateKey: "session", DisableSubagents: true, LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "enabled"}}
	plan, _, err := prepareSessionPlan(t.Context(), req, sessionConfig{})
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
