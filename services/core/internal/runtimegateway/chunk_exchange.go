package runtimegateway

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// exchangeChunkFrame sends once and waits for the owning transfer's next receipt.
// Callers validate their own frame and receipt; no transport outcome is retried.
func (s *Session) exchangeChunkFrame(ctx context.Context, env proto.Envelope, replies <-chan proto.Envelope) (proto.Envelope, error) {
	if err := s.Send(ctx, env); err != nil {
		return proto.Envelope{}, err
	}
	select {
	case reply, ok := <-replies:
		if !ok {
			return proto.Envelope{}, ErrSessionClosed
		}
		return reply, nil
	case <-ctx.Done():
		return proto.Envelope{}, ctx.Err()
	case <-s.closed:
		return proto.Envelope{}, ErrSessionClosed
	}
}
