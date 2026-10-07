package dispatch_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

type durableSteeringSession struct {
	*steeringSession
	phased func(context.Context, proto.PromptSteerPayload, func()) error
}

func (s *durableSteeringSession) SteerWithReceipt(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
	return s.phased(ctx, input, written)
}

func TestDurableSteeringWaitsBeyondTransportDeadline(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	var session *fakeSession
	var calls atomic.Int32
	release := make(chan struct{})
	registerSession(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Steering: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported})}, func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		session = &fakeSession{out: out, closeOutOnCancel: true}
		return &durableSteeringSession{steeringSession: &steeringSession{fakeSession: session}, phased: func(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
			calls.Add(1)
			written()
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, nil
	})
	startRun(t, h.router, h.sender, "codex", "durable")
	input := proto.PromptSteerPayload{InputID: "extra", Input: proto.TextInput("additional"), DurableReceipt: true}
	env := scoped(t, "durable", proto.TypePromptSteer, "durable", input)
	if err := handleSteeringAndWait(t, h, env); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "durable", "extra"); !ack.Written || ack.Accepted || ack.ErrorCode != "" {
		t.Fatalf("write phase: %+v", ack)
	}
	time.Sleep(11 * time.Second)
	if len(h.sender.typesFor("durable")) != 1 {
		t.Fatal("native wait ended at transport deadline")
	}
	if err := handleSteeringAndWait(t, h, env); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "durable", "extra"); !ack.Written || ack.Accepted {
		t.Fatalf("cached phase: %+v", ack)
	}
	close(release)
	waitFor(t, func() bool { return len(h.sender.typesFor("durable")) == 3 }, "native acceptance")
	if ack := lastSteeringAck(t, h.sender, "durable", "extra"); !ack.Accepted || ack.Written {
		t.Fatalf("final phase: %+v", ack)
	}
	session.out <- mustEnv(t, proto.TypeDone, "durable", proto.DonePayload{})
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "completion")
	frames := h.sender.typesFor("durable")
	if calls.Load() != 1 || frames[len(frames)-1] != proto.TypeDone {
		t.Fatal("replayed input or incorrect completion order")
	}
}

func TestDurableSteeringTransportTimeoutAndShutdown(t *testing.T) {
	for _, phase := range []string{"blocked-write", "written"} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			exited := make(chan struct{})
			registerSession(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Steering: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported})}, func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				return &durableSteeringSession{steeringSession: &steeringSession{fakeSession: &fakeSession{out: out, closeOutOnCancel: true}}, phased: func(ctx context.Context, _ proto.PromptSteerPayload, written func()) error {
					defer close(exited)
					if phase == "written" {
						written()
					}
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			})
			ctx := context.Background()
			startRun(t, h.router, h.sender, "codex", "run")
			if err := h.router.Handle(ctx, scoped(t, "run", proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "one", Input: proto.TextInput("text"), DurableReceipt: true})); err != nil {
				t.Fatal(err)
			}
			if phase == "blocked-write" {
				select {
				case <-exited:
				case <-time.After(12 * time.Second):
					t.Fatal("blocked write was not bounded")
				}
				waitFor(t, func() bool { return len(h.sender.typesFor("run")) == 1 }, "unknown receipt")
				if ack := lastSteeringAck(t, h.sender, "run", "one"); ack.Written || ack.Accepted || ack.ErrorCode != "outcome_unknown" {
					t.Fatalf("transport uncertainty: %+v", ack)
				}
			} else {
				waitFor(t, func() bool { return len(h.sender.typesFor("run")) == 1 }, "written phase")
				stopCtx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				if err := h.router.Shutdown(stopCtx); err != nil {
					t.Fatal(err)
				}
				select {
				case <-exited:
				default:
					t.Fatal("native waiter leaked")
				}
			}
		})
	}
}
