//go:build linux

package viewloader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestForPresentsTheHostLoader(t *testing.T) {
	fragment, err := For("/bin/sh")
	if errors.Is(err, agent.ErrUnsupportedOperation) {
		t.Skipf("this host's loader layout is unsupported: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if fragment.LibraryPath == "" {
		t.Skip("/bin/sh is static on this host")
	}
	if len(fragment.Closure) != 1 || fragment.LibraryPath != fragment.Closure[0].Path() || len(fragment.Overlays) != 1 || !fragment.Overlays[0].Exec ||
		filepath.Dir(fragment.Overlays[0].Source) != fragment.Closure[0].HostDir {
		t.Fatalf("fragment = %+v, want the interpreter overlaid from the library mount", fragment)
	}
	if info, err := os.Stat(fragment.Overlays[0].Source); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("interpreter source: %v", err)
	}
	if !slices.Equal(fragment.Masks, []agent.ViewMask{{Path: "/etc/ld.so.preload"}, {Path: "/etc/ld.so.cache"}}) {
		t.Fatalf("masks = %+v", fragment.Masks)
	}
	view := agent.View{Proxy: agent.ViewProxyNone, LocalExec: []string{fragment.Overlays[0].Path},
		Executor: func(context.Context, proto.PromptRequestPayload, agent.ViewSession) (agent.Executor, error) {
			return nil, nil
		}}
	fragment.AddTo(&view)
	if err := view.Validate(); err != nil {
		t.Fatal(err)
	}
}
