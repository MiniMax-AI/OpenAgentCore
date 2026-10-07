package runtimegateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Bind binds the Session's assignment to this connection's Runtime: it sends
// assignment_bind and waits for bound, once per connection and reference.
// Every Session operation on the connection follows its Bind.
func (s *Session) Bind(ctx context.Context, ref proto.AssignmentRef, environmentID string) error {
	s.assignmentMu.Lock()
	bound := s.assignments[ref.SessionID] == ref
	s.assignmentMu.Unlock()
	if bound {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, err := s.exchangeAssignment(ctx, proto.TypeAssignmentBind, ref, proto.AssignmentBindPayload{EnvironmentID: environmentID})
	if err != nil {
		return err
	}
	if status.State != proto.AssignmentBound {
		return fmt.Errorf("agentdaemon gateway: assignment bind failed: %s", status.ErrorCode)
	}
	s.assignmentMu.Lock()
	s.assignments[ref.SessionID] = ref
	s.assignmentMu.Unlock()
	return nil
}

// Release ends the assignment on this connection's Runtime and returns the
// Runtime's status. Only a Runtime that declares home removal accepts
// removeHome.
func (s *Session) Release(ctx context.Context, ref proto.AssignmentRef, removeHome bool) (proto.AssignmentStatusPayload, error) {
	s.assignmentMu.Lock()
	delete(s.assignments, ref.SessionID)
	s.assignmentMu.Unlock()
	return s.exchangeAssignment(ctx, proto.TypeAssignmentRelease, ref, proto.AssignmentReleasePayload{RemoveHome: removeHome})
}

// RemovesHomes reports the Runtime's declaration of home removal; known is
// false before its first heartbeat.
func (s *Session) RemovesHomes() (supported, known bool) {
	s.kindsMu.RLock()
	defer s.kindsMu.RUnlock()
	return s.homeRemoval.IsSupported(), s.kindsSeen
}

func (s *Session) exchangeAssignment(ctx context.Context, typ string, ref proto.AssignmentRef, payload any) (proto.AssignmentStatusPayload, error) {
	var status proto.AssignmentStatusPayload
	if !ref.Valid() {
		return status, errors.New("agentdaemon gateway: invalid assignment")
	}
	id := uuid.NewString()
	env, err := proto.NewEnvelope(typ, id, payload)
	if err != nil {
		return status, err
	}
	env.Assignment = ref
	replies := make(chan proto.Envelope, 1)
	s.assignmentMu.Lock()
	if s.IsClosed() {
		s.assignmentMu.Unlock()
		return status, ErrSessionClosed
	}
	s.assignmentReplies[id] = replies
	s.assignmentMu.Unlock()
	defer func() { s.assignmentMu.Lock(); delete(s.assignmentReplies, id); s.assignmentMu.Unlock() }()
	reply, err := s.exchangeChunkFrame(ctx, env, replies)
	if err != nil {
		return status, err
	}
	if reply.Type != proto.TypeAssignmentStatus || reply.Assignment != ref || reply.DecodePayload(&status) != nil || !validAssignmentStatus(typ, status) {
		return proto.AssignmentStatusPayload{}, errors.New("agentdaemon gateway: invalid assignment status")
	}
	return status, nil
}

func validAssignmentStatus(typ string, status proto.AssignmentStatusPayload) bool {
	switch status.State {
	case proto.AssignmentFailed:
		return status.ErrorCode != ""
	case proto.AssignmentBound:
		return typ == proto.TypeAssignmentBind && status.ErrorCode == ""
	case proto.AssignmentReleased, proto.AssignmentHomeRemoved:
		return typ == proto.TypeAssignmentRelease && status.ErrorCode == ""
	}
	return false
}

func (s *Session) dispatchAssignmentStatus(env proto.Envelope) {
	s.assignmentMu.Lock()
	defer s.assignmentMu.Unlock()
	if replies := s.assignmentReplies[env.ID]; replies != nil {
		select {
		case replies <- env:
		default:
		}
	}
}

func (s *Session) closeAssignmentReplies() {
	s.assignmentMu.Lock()
	defer s.assignmentMu.Unlock()
	for id, replies := range s.assignmentReplies {
		close(replies)
		delete(s.assignmentReplies, id)
	}
}
