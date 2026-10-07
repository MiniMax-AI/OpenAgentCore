//go:build !linux

package agenthost

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Open reports that the agent host needs Linux.
func Open(Config) (*Host, error) {
	return nil, fmt.Errorf("%w: open", ErrUnsupported)
}

type owners struct{}

// Registry holds no kind: the agent host needs Linux.
func (*Host) Registry() *agent.Registry { return agent.NewRegistry() }

// Environments serves no Session: the agent host needs Linux.
func (*Host) Environments(proto.AssignmentRef, proto.AssignmentBindPayload) dispatch.Environment {
	return nil
}

// RemoveHome reports that the agent host needs Linux.
func (*Host) RemoveHome(string) error {
	return fmt.Errorf("%w: remove home", ErrUnsupported)
}
