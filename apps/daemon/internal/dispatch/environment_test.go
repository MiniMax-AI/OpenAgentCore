package dispatch_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

// assertPreparationOutcome waits for the terminal admission status of id. An
// admitted request fails in the controlled factory; a rejected one sends only
// its rejection.
func assertPreparationOutcome(t *testing.T, sender *recSender, id string, admitted bool) proto.PreparationStatusPayload {
	t.Helper()
	state, frames := "rejected", 1
	if admitted {
		state, frames = "failed", 2
	}
	status := waitPreparationStatus(t, sender, id, state, "")
	if got := sender.typesFor(id); len(got) != frames {
		t.Fatalf("preparation %s frames = %v, want %d status frames", id, got, frames)
	}
	return status
}

func TestNoEnvironmentRejectsOtherEngineBeforeFactory(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	var called atomic.Bool
	registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "fake_alpha", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		called.Store(true)
		return nil, errors.New("controlled factory stop")
	})
	assign(t, h.router, preparationSessionID, "")
	err := h.router.Handle(context.Background(), mustEnv(t, proto.TypeExecutionPrepare, "none", noEnvironmentPreparation(preparationSessionID, proto.PromptRequestPayload{AgentKind: "fake_alpha"})))
	if err == nil {
		t.Fatal("unsupported engine was admitted")
	}
	if status := assertPreparationOutcome(t, h.sender, "none", false); status.ErrorCode != "unsupported_configuration" || called.Load() {
		t.Fatalf("unsupported engine was started: status=%+v called=%t", status, called.Load())
	}
}

func TestNoEnvironmentUsesAvailableCapability(t *testing.T) {
	for _, available := range []bool{false, true} {
		h := newHarness(t)
		defer h.router.Shutdown(context.Background())
		var called atomic.Bool
		registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "claude_sdk", Available: available, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})}, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
			called.Store(true)
			return nil, errors.New("controlled factory stop")
		})
		assign(t, h.router, preparationSessionID, "")
		_ = h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "sdk", noEnvironmentPreparation(preparationSessionID, proto.PromptRequestPayload{AgentKind: "claude_sdk"})))
		assertPreparationOutcome(t, h.sender, "sdk", available)
		if called.Load() != available {
			t.Fatalf("factory called=%t, available=%t", called.Load(), available)
		}
	}
}

func TestLocalEnvironmentRequiresAvailableCapability(t *testing.T) {
	for _, mode := range []string{"unsupported", "unavailable", "none conflict", "supported"} {
		t.Run(mode, func(t *testing.T) {
			h := localPreparationHarness(t)
			defer h.router.Shutdown(context.Background())
			var called atomic.Bool
			registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: mode != "unavailable",
				Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilityFromBool(mode != "unsupported")})},
				func(_ context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
					called.Store(true)
					if req.LocalEnvironment == nil || req.LocalEnvironment.ID != preparationEnvironmentID {
						t.Error("local descriptor lost before factory")
					}
					return nil, errors.New("controlled factory stop")
				})
			req := preparationRequest()
			req.Configuration.AgentKind = "codex"
			req.Configuration.DisableExecutionEnvironment = mode == "none conflict"
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "local", req))
			if (err == nil) != (mode == "supported") {
				t.Fatalf("wrong admission for %s: %v", mode, err)
			}
			assertPreparationOutcome(t, h.sender, "local", mode == "supported")
			if called.Load() != (mode == "supported") {
				t.Fatalf("unexpected factory call for %s", mode)
			}
		})
	}
}
