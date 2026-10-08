package dispatch_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// A request that passes configuration admission but whose Environment the
// owner cannot prepare, as with an unreadable capability source, fails before
// the native Executor is created.
func TestRuntimePreparationUnavailablePreventsNativeExecutor(t *testing.T) {
	sender := &recSender{}
	var calls atomic.Int32
	owner := newTestOwner(preparationEnvironmentID, preparationSessionID)
	owner.prepare = func(r agent.PrepareRequest) (agent.PrepareRequest, error) { return r, agentcapabilities.ErrInvalid }
	r := ownedPreparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (preparedFixture, error) {
		calls.Add(1)
		return nil, errors.New("native factory must not be reached")
	}, owner)
	request := preparationRequest()
	request.Configuration.LocalEnvironment.CapabilitySources = &agentcapabilities.Input{Directories: []string{"/capabilities/missing"}}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "missing-capability", request)); err != nil {
		t.Fatalf("valid frozen selection rejected before preparation: %v", err)
	}
	status := waitPreparationStatus(t, sender, "missing-capability", "failed", "")
	if status.ErrorCode != "preparation_failed" || calls.Load() != 0 || r.ActiveRuns() != 0 {
		t.Fatalf("failed capability preparation reached native execution: status=%+v calls=%d", status, calls.Load())
	}
}
