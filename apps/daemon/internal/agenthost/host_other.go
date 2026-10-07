//go:build !linux

package agenthost

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Open reports that the agent host needs Linux.
func Open(Config) (*Host, error) {
	return nil, fmt.Errorf("%w: open", ErrUnsupported)
}

// Registry holds no kind: the agent host needs Linux.
func (*Host) Registry(func(proto.PromptRequestPayload) (Binding, Environment, error)) *agent.Registry {
	return agent.NewRegistry()
}

// RemoveHome reports that the agent host needs Linux.
func (*Host) RemoveHome(sandboxwire.ID) error {
	return fmt.Errorf("%w: remove home", ErrUnsupported)
}
