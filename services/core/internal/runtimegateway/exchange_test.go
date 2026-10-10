package runtimegateway

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestFramesMustNameTheRequestAssignment(t *testing.T) {
	foreign := testAssignment
	foreign.AssignmentID = "foreign"
	for name, ref := range map[string]proto.AssignmentRef{"missing": {}, "foreign": foreign} {
		t.Run(name, func(t *testing.T) {
			s := NewSession(newFakeConn(), "device", "tenant", "test", NewRegistry(), nil)
			defer s.Close("test")
			read := make(chan error, 1)
			go func() {
				_, err := s.ListWorkspaceDirectory(t.Context(), testAssignment, workspaceReadRequest())
				read <- err
			}()
			request := <-s.sendCh
			reply, _ := request.Reply(proto.TypeWorkspaceReadResult, proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true})
			reply.Assignment = ref
			s.dispatch(reply)
			if err := <-read; !errors.Is(err, errAssignmentEcho) {
				t.Fatalf("read = %v", err)
			}

			ack := make(chan error, 1)
			decision, _ := proto.NewEnvelope(proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "delivery"})
			decision.Assignment = testAssignment
			go func() { _, err := s.SendAndWaitInteractionAck(t.Context(), decision, "delivery"); ack <- err }()
			request = <-s.sendCh
			reply, _ = request.Reply(proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: "delivery", Applied: true})
			reply.Assignment = ref
			s.dispatch(reply)
			if err := <-ack; !errors.Is(err, errAssignmentEcho) {
				t.Fatalf("ack = %v", err)
			}

			sub, err := s.SubscribeDurable("run", testAssignment)
			if err != nil {
				t.Fatal(err)
			}
			s.dispatch(proto.Envelope{Type: proto.TypeDelta, ID: "run", Assignment: ref})
			for range sub.Events {
				t.Fatal("frame of another assignment delivered")
			}
			if !errors.Is(sub.Err(), errAssignmentEcho) {
				t.Fatalf("subscription = %v", sub.Err())
			}
		})
	}
}

func TestInteractionConfirmationBudgets(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		delay      time.Duration
		timeout    time.Duration
		applied    bool
		code       string
		wantErr    bool
	}{
		{name: "cancel delayed confirmation", kind: proto.TypePromptCancel, delay: 17 * time.Second, applied: true},
		{name: "cancel early failure", kind: proto.TypePromptCancel, delay: time.Second, code: "cancel_failed"},
		{name: "cancel hangs", kind: proto.TypePromptCancel, delay: 31 * time.Second, wantErr: true},
		{name: "function unchanged", kind: proto.TypeFunctionResult, delay: 16 * time.Second, wantErr: true},
		{name: "caller deadline wins", kind: proto.TypePromptCancel, delay: 4 * time.Second, timeout: 3 * time.Second, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := NewSession(newFakeConn(), "device", "tenant", "test", NewRegistry(), nil)
				defer s.Close("test")
				frame, _ := proto.NewEnvelope(tc.kind, "run", proto.PromptCancelPayload{DeliveryID: "delivery"})
				frame.Assignment = testAssignment
				ctx := t.Context()
				if tc.timeout != 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.timeout)
					defer cancel()
				}
				result := make(chan error, 1)
				started := time.Now()
				go func() {
					ack, err := s.SendAndWaitInteractionAck(ctx, frame, "delivery")
					if err == nil && (ack.Applied != tc.applied || ack.ErrorCode != tc.code) {
						t.Error("receipt changed", ack)
					}
					result <- err
				}()
				request := <-s.sendCh
				stopReply := make(chan struct{})
				defer close(stopReply)
				go func() {
					select {
					case <-time.After(tc.delay):
					case <-stopReply:
						return
					}
					reply, _ := request.Reply(proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: "delivery", Applied: tc.applied, ErrorCode: tc.code})
					s.dispatch(reply)
				}()
				err := <-result
				wantDuration := tc.delay
				if tc.wantErr {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("expected deadline", err)
					}
					wantDuration = proto.CancellationConfirmationTimeout
					if tc.kind == proto.TypeFunctionResult {
						wantDuration = 15 * time.Second
					}
					if tc.timeout != 0 {
						wantDuration = tc.timeout
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(started); elapsed != wantDuration {
					t.Fatalf("elapsed %v, want %v", elapsed, wantDuration)
				}
			})
		})
	}
}
