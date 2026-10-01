//go:build !linux

package claudesdk

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func closureLoader(...string) (viewLoader, error) {
	return viewLoader{}, fmt.Errorf("%w: agent-host views run on Linux", agent.ErrUnsupportedOperation)
}
