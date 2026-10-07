package dispatch

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// handlePromptCancel cancels the run that env.Assignment started. It admits a
// released assignment, so a cancellation of admitted work keeps its receipt.
func (r *Router) handlePromptCancel(ctx context.Context, env proto.Envelope) error {
	var request proto.PromptCancelPayload
	if err := env.DecodeRequest(&request); err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	state := r.sessions[env.ID]
	if state == nil || state.assignment != env.Assignment {
		r.mu.Unlock()
		ack := proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, ErrorCode: "run_inactive"}
		if state != nil {
			ack.ErrorCode = proto.AssignmentConflict
		}
		return r.sendCancellationAck(ctx, env, ack)
	}
	handoff := state.preparedHandoff
	release, attempt := r.claimPreparedReleaseLocked(state, true, "", true)
	if request.DeliveryID != "" {
		done := r.trackWorkLocked(env.Assignment)
		r.shutdownWG.Add(1)
		go func() {
			defer done()
			r.sendPreparedCancellation(state, handoff, release, attempt, env, request.DeliveryID)
		}()
	}
	r.mu.Unlock()
	return nil
}

func (r *Router) sendCancellationAck(ctx context.Context, env proto.Envelope, ack proto.InteractionDecisionAckPayload) error {
	if ack.DeliveryID == "" {
		return nil
	}
	reply, err := env.Reply(proto.TypeInteractionDecisionAck, ack)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, reply)
}
