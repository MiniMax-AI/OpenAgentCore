//go:build !unix

package mcode

import (
	"fmt"
	"os/exec"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// runAsOwner is unavailable: agent-host views run on Linux.
func runAsOwner(*exec.Cmd, string) error {
	return fmt.Errorf("%w: mcode agent-host views run on Linux", agent.ErrUnsupportedOperation)
}
