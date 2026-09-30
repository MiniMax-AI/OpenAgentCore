package cli

import (
	"strings"
	"testing"
)

func TestRetiredRuntimeSettingsRejectBeforeEverySubcommand(t *testing.T) {
	t.Setenv("PARSAR_HOME", "private-old-home")
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	t.Setenv("PARSAR_UNKNOWN_RUNTIME_SETTING", "private-canary")
	t.Setenv("PARSAR_CODEX_BIN", "")
	t.Setenv("PARSAR_MCP_BEARER_EXAMPLE", "private-bearer")
	for _, command := range []string{"version", "--help", "connect", "runtime-mcp-exec"} {
		stdout, stderr, err := runArgv(t, command)
		if err == nil || stdout != "" || stderr != "" {
			t.Fatalf("%s ran before retirement validation: %q %q %v", command, stdout, stderr, err)
		}
		for _, wanted := range []string{"PARSAR_HOME was renamed to OAC_RUNTIME_HOME", "PARSAR_CODEX_BIN was renamed to OAC_RUNTIME_CODEX_BIN", "PARSAR_MCP_BEARER_EXAMPLE was renamed to OAC_RUNTIME_MCP_BEARER_EXAMPLE", "PARSAR_UNKNOWN_RUNTIME_SETTING is not read by OpenAgentCore"} {
			if !strings.Contains(err.Error(), wanted) {
				t.Fatalf("missing retirement diagnostic %q: %v", wanted, err)
			}
		}
		if strings.Contains(err.Error(), "private-") {
			t.Fatal("retirement diagnostic exposed a setting value")
		}
	}
}

func TestProductSettingsRemainSeparateFromRuntimeRetirement(t *testing.T) {
	for _, name := range []string{"PARSAR_CAPABILITY_UPLOAD_TOKEN", "PARSAR_SERVER_URL", "PARSAR_MASTER_KEY", "PARSAR_PROTOCOL_BASELINE_REVISION"} {
		t.Setenv(name, "product-only")
	}
	stdout, _, err := runArgv(t, "version")
	if err != nil || !strings.Contains(stdout, Version) {
		t.Fatalf("product setting blocked daemon: %v", err)
	}
}
