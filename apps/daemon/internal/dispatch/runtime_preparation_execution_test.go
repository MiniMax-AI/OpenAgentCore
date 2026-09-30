package dispatch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Directory selection is syntactically valid and passes frozen configuration
// admission. Runtime preparation must fail before creating a native Executor.
func TestRuntimePreparationUnavailablePreventsNativeExecutor(t *testing.T) {
	for _, mode := range []string{"missing-directory", "invalid-skill"} {
		t.Run(mode, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "capability")
			if mode == "invalid-skill" {
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("not a portable Skill"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			sender := &recSender{}
			var calls atomic.Int32
			r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
				calls.Add(1)
				return nil, errors.New("native factory must not be reached")
			})
			installation := os.Getenv("OAC_RUNTIME_CAPABILITY_DIRECTORY")
			if info, err := os.Stat(installation); err != nil || !info.IsDir() {
				t.Fatalf("capability fixture is unavailable: %v", err)
			}
			request := preparationRequest()
			request.Configuration.LocalEnvironment.Capabilities = true
			request.Configuration.LocalEnvironment.CapabilitySources = &agentcapabilities.Input{Directories: []string{source}}
			if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "missing-capability", request)); err != nil {
				t.Fatalf("valid frozen selection rejected before preparation: %v", err)
			}
			status := waitPreparationStatus(t, sender, "missing-capability", "failed", "")
			if _, err := os.Stat(filepath.Join(installation, agentcapabilities.ManifestName)); !os.IsNotExist(err) {
				t.Fatal("invalid source published an installed snapshot")
			}
			if status.ErrorCode != "preparation_failed" || calls.Load() != 0 || r.ActiveRuns() != 0 {
				t.Fatalf("failed capability preparation reached native execution: status=%+v calls=%d", status, calls.Load())
			}
		})
	}
}
