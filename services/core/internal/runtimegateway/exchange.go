package runtimegateway

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var errAssignmentEcho = errors.New("agentdaemon gateway: frame names another assignment")

// exchangeFrame sends env once and waits for the reply its owner routes to
// replies. A reply that arrived before the deadline or the connection closed
// still counts. The reply must echo env's assignment. Callers validate their
// own frame and receipt; no transport outcome is retried.
func (s *Session) exchangeFrame(ctx context.Context, env proto.Envelope, replies <-chan proto.Envelope) (proto.Envelope, error) {
	if err := s.Send(ctx, env); err != nil {
		return proto.Envelope{}, err
	}
	var reply proto.Envelope
	ok := true
	select {
	case reply, ok = <-replies:
	case <-ctx.Done():
		select {
		case reply, ok = <-replies:
		default:
			return proto.Envelope{}, ctx.Err()
		}
	case <-s.closed:
		select {
		case reply, ok = <-replies:
		default:
			return proto.Envelope{}, ErrSessionClosed
		}
	}
	if !ok {
		return proto.Envelope{}, ErrSessionClosed
	}
	if reply.Assignment != env.Assignment {
		return proto.Envelope{}, errAssignmentEcho
	}
	return reply, nil
}
