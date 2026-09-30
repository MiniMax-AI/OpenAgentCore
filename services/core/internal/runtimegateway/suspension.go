package runtimegateway

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// SuspendControl correlates one lifecycle operation on this authenticated socket.
// A closed socket is not an acknowledgement; callers retain their durable intent.
func (s *Session) SuspendControl(ctx context.Context, kind string, request proto.EnvironmentSuspendPayload) (proto.EnvironmentSuspendResultPayload, error) {
	var result proto.EnvironmentSuspendResultPayload
	expected := proto.TypeEnvironmentQuiesced
	if kind == proto.TypeEnvironmentResume {
		expected = proto.TypeEnvironmentResumed
	} else if kind != proto.TypeEnvironmentQuiesce {
		return result, errors.New("invalid suspension operation")
	}
	if request.EnvironmentID == "" || request.SuspendID == "" {
		return result, errors.New("invalid suspension identity")
	}
	id := uuid.NewString()
	envelope, err := proto.NewEnvelope(kind, id, request)
	if err != nil {
		return result, err
	}
	s.suspendMu.Lock()
	if s.IsClosed() {
		s.suspendMu.Unlock()
		return result, ErrSessionClosed
	}
	if len(s.suspendReplies) >= 2 {
		s.suspendMu.Unlock()
		return result, errors.New("suspension request capacity")
	}
	if s.suspendReplies == nil {
		s.suspendReplies = make(map[string]chan proto.Envelope)
	}
	replies := make(chan proto.Envelope, 1)
	s.suspendReplies[id] = replies
	s.suspendMu.Unlock()
	defer func() { s.suspendMu.Lock(); delete(s.suspendReplies, id); s.suspendMu.Unlock() }()
	if err := s.Send(ctx, envelope); err != nil {
		return result, err
	}
	// The daemon closes after quiesced. Read the buffered result before treating
	// connection closure as unknown, including when both channels become ready.
	decode := func(reply proto.Envelope, ok bool) (proto.EnvironmentSuspendResultPayload, error) {
		if !ok {
			return result, ErrSessionClosed
		}
		if reply.Type != expected || reply.DecodePayload(&result) != nil || result.EnvironmentID != request.EnvironmentID || result.SuspendID != request.SuspendID || (result.Accepted && result.ErrorCode != "") {
			return proto.EnvironmentSuspendResultPayload{}, errors.New("invalid suspension acknowledgement")
		}
		return result, nil
	}
	select {
	case reply, ok := <-replies:
		return decode(reply, ok)
	case <-ctx.Done():
		return result, ctx.Err()
	case <-s.closed:
		select {
		case reply, ok := <-replies:
			return decode(reply, ok)
		default:
			return result, ErrSessionClosed
		}
	}
}

func (s *Session) dispatchSuspendReply(env proto.Envelope) {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()
	if replies := s.suspendReplies[env.ID]; replies != nil {
		select {
		case replies <- env:
		default:
		}
	}
}
func (s *Session) closeSuspendReplies() {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()
	for id, replies := range s.suspendReplies {
		close(replies)
		delete(s.suspendReplies, id)
	}
}
