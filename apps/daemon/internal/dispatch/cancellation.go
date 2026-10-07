package dispatch

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

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
	if state == nil {
		r.mu.Unlock()
		return r.sendCancellationAck(ctx, env, proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, ErrorCode: "run_inactive"})
	}
	handoff := state.preparedHandoff
	release, attempt := r.claimPreparedReleaseLocked(state, true, "", true)
	if request.DeliveryID != "" {
		r.shutdownWG.Add(1)
		go r.sendPreparedCancellation(state, handoff, release, attempt, env, request.DeliveryID)
	}
	r.mu.Unlock()
	return nil
}

func (r *Router) sendCancellationAck(ctx context.Context, env proto.Envelope, ack proto.InteractionDecisionAckPayload) error {
	if ack.DeliveryID == "" {
		return nil
	}
	reply, err := proto.NewEnvelopeWithTrace(proto.TypeInteractionDecisionAck, env.ID, ack, env.Trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, reply)
}
