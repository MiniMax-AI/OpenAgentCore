//go:build !linux

package viewloader

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// For reports that agent-host views run only on Linux.
func For(...string) (Fragment, error) {
	return Fragment{}, fmt.Errorf("%w: agent-host views run on Linux", agent.ErrUnsupportedOperation)
}
