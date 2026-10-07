package runtimegateway

import (
	"errors"
	"testing"

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
			go func() { _, err := s.ReadWorkspaceFile(t.Context(), testAssignment, workspaceReadRequest()); read <- err }()
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
