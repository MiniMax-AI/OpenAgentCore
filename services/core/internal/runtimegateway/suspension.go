package runtimegateway

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// SuspendControl correlates one lifecycle operation on this authenticated socket.
// A closed socket is not an acknowledgement; callers retain their durable intent.
// The reference names the Session's assignment.
func (s *Session) SuspendControl(ctx context.Context, kind string, ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload) (proto.EnvironmentSuspendResultPayload, error) {
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
	envelope.Assignment = ref
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
	// The daemon closes after quiesced; the exchange reads a result that
	// arrived before the closure.
	reply, err := s.exchangeFrame(ctx, envelope, replies)
	if err != nil {
		return result, err
	}
	if reply.Type != expected || reply.DecodePayload(&result) != nil || result.EnvironmentID != request.EnvironmentID || result.SuspendID != request.SuspendID || (result.Accepted && result.ErrorCode != "") {
		return proto.EnvironmentSuspendResultPayload{}, errors.New("invalid suspension acknowledgement")
	}
	return result, nil
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
