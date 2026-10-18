package pi

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/contracttest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestSharedTextLifecycle(t *testing.T) {
	fakePi(t)
	owner := prepareTestExecutor(t)
	native := owner.(*executor).sessionID
	contracttest.TextLifecycle(t, contracttest.TextFixture{
		Executor:      owner,
		CompleteInput: proto.TextInput("hello"),
		ActiveInput:   proto.TextInput("wait"),
		SteeringInput: proto.TextInput("continue waiting"),
		Ready:         func(event proto.Envelope) bool { return event.Type == proto.TypeDelta },
		NativeOwner:   func() string { return native },
	})
}
