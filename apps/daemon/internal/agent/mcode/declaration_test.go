package mcode

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// An available runtime is execution-capable; a rejected native version is unavailable.
func TestMCodeExecutionFollowsAvailability(t *testing.T) {
	for _, tc := range []struct {
		version string
		check   error
	}{{SupportedVersion, nil}, {"0.3.11", errors.New("mcode: unsupported version 0.3.11")}} {
		t.Run(tc.version, func(t *testing.T) {
			rc := agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}
			runtime := discoverWithCheck(t.Context(), rc, Declaration.Info, func(context.Context, string) (string, error) { return tc.version, tc.check })
			info, available := runtime.Info, tc.check == nil
			if (runtime.Executor != nil) != available {
				t.Fatalf("factories: %+v", runtime)
			}
			if info.Available != available {
				t.Fatalf("available=%v", info.Available)
			}
		})
	}
}
