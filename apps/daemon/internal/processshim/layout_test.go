package processshim

import (
	"path"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func TestSocketPathFollowsTheViewLayout(t *testing.T) {
	if want := path.Join(agent.ViewPrivateRoot, agent.ViewRunName, SocketName); SocketPath != want {
		t.Fatalf("SocketPath = %q, want %q", SocketPath, want)
	}
}
