package mcode

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// A rejected native version is unavailable and has no view.
func TestMCodeDiscoveryFollowsAvailability(t *testing.T) {
	for _, tc := range []struct {
		version string
		check   error
	}{{SupportedVersion, nil}, {"0.3.11", errors.New("mcode: unsupported version 0.3.11")}} {
		t.Run(tc.version, func(t *testing.T) {
			rc := agent.DiscoveryOptions{Stdout: io.Discard, Stderr: io.Discard}
			runtime := discoverWithCheck(t.Context(), rc, Declaration.Info, func(context.Context, string) (string, error) { return tc.version, tc.check })
			info, available := runtime.Info, tc.check == nil
			if !available && runtime.View != nil {
				t.Fatalf("unavailable runtime has a view: %+v", runtime)
			}
			if info.Available != available {
				t.Fatalf("available=%v", info.Available)
			}
		})
	}
}
