package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

type steeringSession struct {
	*fakeSession
	steer func(context.Context, proto.PromptSteerPayload, func()) error
}

func (s *steeringSession) SteerWithReceipt(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
	return s.steer(ctx, input, written)
}

func TestSteeringReceiptsAndRetries(t *testing.T) {
	for _, engineError := range []error{nil, errors.New("native connection lost after write")} {
		name := "accepted"
		if engineError != nil {
			name = "uncertain"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			calls, starts := 0, 0
			registerSession(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, func(ctx context.Context, req fixtureRun, out chan<- proto.Envelope) (fixtureSession, error) {
				starts++
				return &steeringSession{
					fakeSession: &fakeSession{out: out, closeOutOnCancel: true},
					steer: func(_ context.Context, input proto.PromptSteerPayload, _ func()) error {
						calls++
						if input.InputID != "input-1" || *input.Input[0].Content[0].Text != "additional text" {
							t.Errorf("input lost: %+v", input)
						}
						if hasFrame(h.sender, proto.TypePromptSteerAck, "run-1") {
							t.Error("ack sent before engine accepted input")
						}
						return engineError
					},
				}, nil
			})
			ctx := context.Background()
			startRun(t, h.router, h.sender, "codex", "run-1")
			input := proto.PromptSteerPayload{InputID: "input-1", Input: proto.TextInput("additional text")}
			env := scoped(t, "run-1", proto.TypePromptSteer, "run-1", input)
			// An ack transport failure must not cause another native invocation.
			h.sender.failNow = true
			if err := h.router.Handle(ctx, env); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { h.sender.mu.Lock(); defer h.sender.mu.Unlock(); return !h.sender.failNow }, "failed ack send")
			for range 2 {
				if err := handleSteeringAndWait(t, h, env); err != nil {
					t.Fatal(err)
				}
				ack := lastSteeringAck(t, h.sender, "run-1", "input-1")
				if ack.Accepted != (engineError == nil) {
					t.Fatalf("receipt: %+v", ack)
				}
				if engineError != nil && ack.ErrorCode != "outcome_unknown" {
					t.Fatalf("uncertainty lost: %+v", ack)
				}
			}
			input.Input = proto.TextInput("changed text")
			if err := handleSteeringAndWait(t, h, scoped(t, "run-1", proto.TypePromptSteer, "run-1", input)); err != nil {
				t.Fatal(err)
			}
			if ack := lastSteeringAck(t, h.sender, "run-1", "input-1"); ack.ErrorCode != "input_conflict" {
				t.Fatalf("conflict: %+v", ack)
			}
			if calls != 1 || starts != 1 {
				t.Fatalf("native calls=%d, runs started=%d", calls, starts)
			}
		})
	}
}

func TestSteeringReadinessAndInputValidation(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	calls := 0
	registerSession(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, func(ctx context.Context, req fixtureRun, out chan<- proto.Envelope) (fixtureSession, error) {
		return &steeringSession{
			fakeSession: &fakeSession{out: out, closeOutOnCancel: true},
			steer: func(context.Context, proto.PromptSteerPayload, func()) error {
				calls++
				if calls == 1 {
					return agent.ErrSteeringNotReady
				}
				return nil
			},
		}, nil
	})
	ctx := context.Background()
	startRun(t, h.router, h.sender, "codex", "run-1")
	input := proto.PromptSteerPayload{InputID: "input-1", Input: proto.TextInput("extra")}
	env := scoped(t, "run-1", proto.TypePromptSteer, "run-1", input)
	for _, expected := range []string{"not_ready", ""} {
		if err := handleSteeringAndWait(t, h, env); err != nil {
			t.Fatal(err)
		}
		if ack := lastSteeringAck(t, h.sender, "run-1", "input-1"); ack.ErrorCode != expected {
			t.Fatalf("expected %q: %+v", expected, ack)
		}
		if expected == "not_ready" {
			changed := proto.PromptSteerPayload{InputID: "input-1", Input: proto.TextInput("different during startup")}
			if err := handleSteeringAndWait(t, h, scoped(t, "run-1", proto.TypePromptSteer, "run-1", changed)); err != nil {
				t.Fatal(err)
			}
			if ack := lastSteeringAck(t, h.sender, "run-1", "input-1"); ack.ErrorCode != "input_conflict" {
				t.Fatalf("startup identity changed: %+v", ack)
			}
		}
	}
	if err := h.router.Handle(ctx, scoped(t, "run-1", proto.TypePromptCancel, "run-1", nil)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "cancel cleanup")
	if err := handleSteeringAndWait(t, h, env); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-1", "input-1"); ack.ErrorCode != "run_inactive" {
		t.Fatalf("inactive: %+v", ack)
	}
	startRun(t, h.router, h.sender, "codex", "run-2")
	input.Input = proto.TextInput("")
	if err := handleSteeringAndWait(t, h, scoped(t, "run-2", proto.TypePromptSteer, "run-2", input)); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-2", "input-1"); ack.ErrorCode != "invalid_input" {
		t.Fatalf("invalid: %+v", ack)
	}
	// Whitespace-only text is content: dispatch forwards it like any other text.
	input.InputID, input.Input = "input-2", proto.TextInput(" \n ")
	if err := handleSteeringAndWait(t, h, scoped(t, "run-2", proto.TypePromptSteer, "run-2", input)); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-2", "input-2"); ack.ErrorCode != "" {
		t.Fatalf("whitespace: %+v", ack)
	}
}

func TestSteeringDoesNotBlockOtherRunCancellation(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	registerSession(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, func(ctx context.Context, req fixtureRun, out chan<- proto.Envelope) (fixtureSession, error) {
		return &steeringSession{
			fakeSession: &fakeSession{out: out, closeOutOnCancel: true},
			steer: func(context.Context, proto.PromptSteerPayload, func()) error {
				close(entered)
				<-release
				return agent.ErrSteeringRejected
			},
		}, nil
	})
	ctx := context.Background()
	for _, run := range []struct{ id, engine string }{{"run-1", "codex"}, {"run-2", "fake_alpha"}} {
		startRun(t, h.router, h.sender, run.engine, run.id)
	}
	other := <-h.gotSess
	returned := make(chan error, 1)
	go func() {
		returned <- h.router.Handle(ctx, scoped(t, "run-1", proto.TypePromptSteer, "run-1", proto.PromptSteerPayload{InputID: "slow", Input: proto.TextInput("extra")}))
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("steering blocked dispatch")
	}
	<-entered
	if err := h.router.Handle(ctx, scoped(t, "run-2", proto.TypePromptCancel, "run-2", nil)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return other.cancels() == 1 }, "other run cancellation")
}

func lastSteeringAck(t *testing.T, sender *recSender, runID, inputID string) proto.PromptSteerAckPayload {
	t.Helper()
	frames := sender.snapshot()
	if len(frames) == 0 {
		t.Fatal("missing ack")
	}
	env := frames[len(frames)-1]
	var ack proto.PromptSteerAckPayload
	if env.Type != proto.TypePromptSteerAck || env.ID != runID {
		t.Fatalf("incorrect routing: %+v", env)
	}
	if err := env.DecodePayload(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.InputID != inputID {
		t.Fatalf("incorrect input: %+v", ack)
	}
	return ack
}

func TestSteeringCapacityPreservesExistingReceipts(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	calls := 0
	registerSession(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, func(ctx context.Context, req fixtureRun, out chan<- proto.Envelope) (fixtureSession, error) {
		return &steeringSession{
			fakeSession: &fakeSession{out: out, closeOutOnCancel: true},
			steer: func(context.Context, proto.PromptSteerPayload, func()) error {
				calls++
				return nil
			},
		}, nil
	})
	startRun(t, h.router, h.sender, "codex", "run-1")
	for i := range 257 {
		input := proto.PromptSteerPayload{InputID: fmt.Sprintf("input-%d", i), Input: proto.TextInput("extra")}
		if err := handleSteeringAndWait(t, h, scoped(t, "run-1", proto.TypePromptSteer, "run-1", input)); err != nil {
			t.Fatal(err)
		}
		ack := lastSteeringAck(t, h.sender, "run-1", input.InputID)
		if i < 256 && !ack.Accepted || i == 256 && ack.ErrorCode != "input_limit" {
			t.Fatalf("input %d: %+v", i, ack)
		}
	}
	if err := handleSteeringAndWait(t, h, scoped(t, "run-1", proto.TypePromptSteer, "run-1", proto.PromptSteerPayload{InputID: "input-0", Input: proto.TextInput("extra")})); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-1", "input-0"); !ack.Accepted || calls != 256 {
		t.Fatalf("receipt evicted or input redelivered: %+v, calls=%d", ack, calls)
	}
}

func handleSteeringAndWait(t *testing.T, h *harness, env proto.Envelope) error {
	t.Helper()
	before := len(h.sender.snapshot())
	if err := h.router.Handle(context.Background(), env); err != nil {
		return err
	}
	waitFor(t, func() bool { return len(h.sender.snapshot()) > before }, "steering ack")
	return nil
}

func TestSteeringRetainsAdmittedDeclaration(t *testing.T) {
	for _, test := range []struct {
		name              string
		admitted, current proto.CapabilitySupport
		available         bool
	}{
		{"narrowed", proto.CapabilitySupported, proto.CapabilityUnsupported, true},
		{"widened", proto.CapabilityUnsupported, proto.CapabilitySupported, true},
		{"unavailable", proto.CapabilitySupported, proto.CapabilitySupported, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			var calls atomic.Int32
			factory := func(_ context.Context, _ fixtureRun, out chan<- proto.Envelope) (fixtureSession, error) {
				return &steeringSession{fakeSession: &fakeSession{out: out, closeOutOnCancel: true}, steer: func(_ context.Context, input proto.PromptSteerPayload, _ func()) error {
					calls.Add(1)
					if !input.Input.HasImages() {
						t.Error("native input lost its image")
					}
					return nil
				}}, nil
			}
			info := proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{MessageImages: test.admitted})}
			registerSession(h.reg, info, factory)
			startRun(t, h.router, h.sender, "fixture", "active")
			info.Capabilities.MessageImages, info.Available = test.current, test.available
			registerSession(h.reg, info, factory)
			image := "https://example.com/input.png"
			input := proto.PromptSteerPayload{InputID: "image", Input: proto.MessageInput{{Content: []proto.InputContent{{Type: "input_image", ImageURL: &image}}}}}
			env := scoped(t, "active", proto.TypePromptSteer, "active", input)
			accepted := test.admitted.IsSupported()
			code := "unsupported"
			if accepted {
				code = ""
			}
			for range 2 {
				if err := handleSteeringAndWait(t, h, env); err != nil {
					t.Fatal(err)
				}
				if ack := lastSteeringAck(t, h.sender, "active", "image"); ack.Accepted != accepted || ack.Written || ack.ErrorCode != code {
					t.Fatalf("admitted declaration changed: %+v", ack)
				}
			}
			wanted := int32(0)
			if accepted {
				wanted = 1
			}
			if calls.Load() != wanted {
				t.Fatalf("native calls=%d, want %d", calls.Load(), wanted)
			}
			// Rejected and accepted receipts both bind the original input identity.
			changed := input
			changed.Input = proto.TextInput("changed input")
			if err := handleSteeringAndWait(t, h, scoped(t, "active", proto.TypePromptSteer, "active", changed)); err != nil {
				t.Fatal(err)
			}
			if ack := lastSteeringAck(t, h.sender, "active", "image"); ack.ErrorCode != "input_conflict" {
				t.Fatalf("receipt identity lost: %+v", ack)
			}
			foreign := env
			foreign.Assignment.AssignmentID = "other"
			if err := handleSteeringAndWait(t, h, foreign); err != nil {
				t.Fatal(err)
			}
			if ack := lastSteeringAck(t, h.sender, "active", "image"); ack.ErrorCode != proto.AssignmentConflict {
				t.Fatalf("receipt escaped assignment: %+v", ack)
			}
			if test.available {
				// Another admitted Turn uses the current declaration, without changing
				// the active Turn's permissions or replaying its receipt.
				startRun(t, h.router, h.sender, "fixture", "new")
				if err := handleSteeringAndWait(t, h, scoped(t, "new", proto.TypePromptSteer, "new", input)); err != nil {
					t.Fatal(err)
				}
				accepted = test.current.IsSupported()
				code = "unsupported"
				if accepted {
					code = ""
					wanted++
				}
				if ack := lastSteeringAck(t, h.sender, "new", "image"); ack.Accepted != accepted || ack.ErrorCode != code {
					t.Fatalf("new Turn ignored current declaration: %+v", ack)
				}
			} else {
				assign(t, h.router, "new", "")
				prepare := scoped(t, "new", proto.TypeExecutionPrepare, "prepare-new", noEnvironmentPreparation("new", proto.PromptRequestPayload{AgentKind: "fixture"}))
				if err := h.router.Handle(t.Context(), prepare); err == nil {
					t.Fatal("new admission accepted an unavailable kind")
				}
				if status := waitPreparationStatus(t, h.sender, prepare.ID, "rejected", ""); status.ErrorCode != "resource_unavailable" {
					t.Fatalf("new admission ignored unavailability: %+v", status)
				}
			}
			if calls.Load() != wanted {
				t.Fatalf("native calls=%d, want %d", calls.Load(), wanted)
			}
		})
	}
}
