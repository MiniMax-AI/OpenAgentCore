package claudesdk

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Cancel addresses this fixed Turn. Settlement remains observable after the caller detaches.
func (s *session) Cancel(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-s.settled:
		return s.settlementErr
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.owner == nil {
		return errors.New("claudesdk: Turn owner is unavailable")
	}
	s.cancelOnce.Do(func() {
		close(s.cancelOutput)
		s.owner.mu.Lock()
		current := s.owner.active == s && !s.owner.invalid
		s.owner.mu.Unlock()
		if current {
			stopWrite := context.AfterFunc(ctx, s.invalidate)
			defer stopWrite()
			if err := s.owner.write(struct {
				Type   string `json:"type"`
				TurnID string `json:"turn_id"`
			}{"turn_cancel", s.runID}); err != nil {
				s.invalidate()
			}
		}
	})
	select {
	case <-s.settled:
		return s.settlementErr
	case <-ctx.Done():
		select {
		case <-s.settled:
			return s.settlementErr
		default:
			return ctx.Err()
		}
	}
}

// CancellationOutcome is available after successful Cancel or terminal publication.
// Closing settled publishes the immutable snapshot; an unsettled result is unknown.
func (s *session) CancellationOutcome() proto.DonePayload {
	select {
	case <-s.settled:
		return s.outcome
	default:
		return proto.DonePayload{}
	}
}

var _ agent.Turn = (*session)(nil)
