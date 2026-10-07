//go:build !linux

package cli

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agenthost"
)

// runAgentHost reports that the agent host needs Linux.
func runAgentHost(*runContext, []string) error {
	return fmt.Errorf("%w: agent-host", agenthost.ErrUnsupported)
}
