package runtimegateway

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"testing"
)

func TestSuspendAcknowledgementSurvivesImmediateConnectionClose(t *testing.T) {
	for range 100 {
		s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
		done := make(chan error, 1)
		go func() {
			result, err := s.SuspendControl(t.Context(), proto.TypeEnvironmentQuiesce, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"})
			if err == nil && !result.Accepted {
				err = errors.New("ack lost")
			}
			done <- err
		}()
		request := <-s.sendCh
		reply, _ := proto.NewEnvelope(proto.TypeEnvironmentQuiesced, request.ID, proto.EnvironmentSuspendResultPayload{EnvironmentID: "env", SuspendID: "attempt", Accepted: true})
		s.dispatch(reply)
		s.Close("parked")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
func TestSuspendControlRejectsForeignIdentityAndDoesNotReplay(t *testing.T) {
	for _, result := range []proto.EnvironmentSuspendResultPayload{
		{EnvironmentID: "other", SuspendID: "attempt", Accepted: true},
		{EnvironmentID: "env", SuspendID: "old", Accepted: true},
		{EnvironmentID: "env", SuspendID: "attempt", Accepted: true, ErrorCode: "resource_busy"},
	} {
		s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
		done := make(chan error, 1)
		go func() {
			_, err := s.SuspendControl(t.Context(), proto.TypeEnvironmentResume, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"})
			done <- err
		}()
		request := <-s.sendCh
		reply, _ := proto.NewEnvelope(proto.TypeEnvironmentResumed, request.ID, result)
		s.dispatch(reply)
		if err := <-done; err == nil {
			t.Fatal("foreign receipt accepted")
		}
		select {
		case <-s.sendCh:
			t.Fatal("unexpected replay")
		default:
		}
		s.Close("test")
	}
}
func TestSuspendObserverCancellationRetainsUnknownOutcome(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := s.SuspendControl(ctx, proto.TypeEnvironmentQuiesce, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"})
		done <- err
	}()
	<-s.sendCh
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-s.sendCh:
		t.Fatal("cancellation replayed operation")
	default:
	}
}
