package mcode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Nil concrete receivers prove Unsupported needs no native owner, transport,
// workspace access or input retention. Callers still preserve the separate
// nil-Turn ownership rule on required lifecycle methods.
func TestUnsupportedExtensionsHaveNoNativeEffects(t *testing.T) {
	ctx := context.Background()
	const secret = "private-fixture-value"
	check := func(err error) {
		t.Helper()
		if !errors.Is(err, agent.ErrUnsupportedOperation) || err.Error() == agent.ErrUnsupportedOperation.Error() {
			t.Fatalf("expected explicit unsupported result with reason, got %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("unsupported error disclosed input")
		}
	}
	var turn *Session
	check(turn.SubmitFunctionResult(ctx, proto.FunctionResultPayload{CallID: secret, DeliveryID: secret}))
}
