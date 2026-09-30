//go:build unix

package claudesdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestWorkspaceLaunchAndReadinessInheritsUserEnvironment(t *testing.T) {
	config := workspaceFixture(t)
	t.Setenv("OAC_TEST_PARENT_SECRET", "must-not-inherit")
	t.Setenv("ANTHROPIC_API_KEY", "unselected")
	// An owned process fixture verifies both real subprocess launch paths; it is
	// not a native sandbox or provider acceptance test.
	script := `#!/bin/sh
test "$OAC_TEST_PARENT_SECRET" = must-not-inherit || exit 21
test -z "${ANTHROPIC_API_KEY+x}" || exit 22
test "$ANTHROPIC_AUTH_TOKEN" = selected-provider-fixture || exit 23
test "$TMPDIR" != "$CLAUDE_CONFIG_DIR/tmp" || exit 24
case "$1" in
  */runtime_check.js)
    printf '%s\n' '{"type":"runtime_ready","protocol":3,"node":"fixture","sdk":"fixture","mcp":"fixture","native":"fixture","features":["workspace_tools","workspace_prepare"]}' ;;
  *)
    IFS= read -r request
    printf '%s\n' '{"type":"executor_ready","protocol":3}'
    IFS= read -r request
    printf '%s\n' '{"type":"result","turn_id":"run","session_id":"native","text":"completed"}'
    printf '%s\n' '{"type":"turn_settled","turn_id":"run","confirmed":true,"reusable":true,"reason":""}' ;;
esac
`
	if err := os.WriteFile(config.Node, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := CheckRuntime(t.Context(), config)
	if err != nil || !info.supportsWorkspace() {
		t.Fatal("readiness did not receive replacement environment", err)
	}
	out := make(chan proto.Envelope, 8)
	s, err := NewFactory(config)(t.Context(), workspaceRequest(), out)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel(t.Context())
	done := 0
	for event := range out {
		if event.Type == proto.TypeError {
			t.Fatal("execution fixture rejected replacement environment")
		}
		if event.Type == proto.TypeDone {
			done++
		}
	}
	if done != 1 {
		t.Fatal("expected one settled completion")
	}
	// Feature checking must reject an older bridge without starting execution.
	script = strings.ReplaceAll(script, `"features":["workspace_tools","workspace_prepare"]`, `"features":[]`)
	script = strings.ReplaceAll(script, "IFS= read -r request", "touch '"+filepath.Join(config.StateDir, "unexpected-start")+"'")
	if err := os.WriteFile(config.Node, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFactory(config)(t.Context(), workspaceRequest(), out); err == nil {
		t.Fatal("old packaged bridge accepted workspace execution")
	}
	if _, err := os.Stat(filepath.Join(config.StateDir, "unexpected-start")); !os.IsNotExist(err) {
		t.Fatal("old packaged bridge started execution")
	}
}
