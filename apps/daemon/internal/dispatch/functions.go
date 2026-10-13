package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (r *Router) handleFunctionResult(ctx context.Context, env proto.Envelope) error {
	var result proto.FunctionResultPayload
	if err := env.DecodePayload(&result); err != nil {
		return err
	}
	if env.ID == "" || result.CallID == "" || result.DeliveryID == "" {
		return errors.New("function result requires run, call and delivery identities")
	}
	if err := result.ValidateContent(); err != nil {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "invalid_result", err.Error())
	}
	decision := result
	decision.DeliveryID = ""
	encoded, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	fingerprint := sha256.Sum256(encoded)
	// Scope receipt replay to both identities, even when native call IDs repeat across Runs.
	key := env.ID + "\x00" + result.CallID
	r.mu.Lock()
	applied, replay := r.applied[key]
	r.mu.Unlock()
	if replay {
		if applied.fingerprint != fingerprint {
			return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "decision_conflict", "request was already applied with a different decision")
		}
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, true, "", "")
	}
	r.mu.Lock()
	state := r.sessions[env.ID]
	session, finishOperation, ready := r.preparedOperationLocked(state)
	var submitter agent.FunctionResultSubmitter
	if ready {
		submitter, _ = session.(agent.FunctionResultSubmitter)
	}
	r.mu.Unlock()
	if state != nil && !ready {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "not_ready", "function call is waiting for the native session")
	}
	if finishOperation != nil {
		defer finishOperation()
		var stop context.CancelFunc
		ctx, stop = r.shutdownContext(ctx)
		defer stop()
	}
	if state != nil && !state.capabilities.FunctionTools.IsSupported() {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "unsupported", "The runtime declaration does not support function results.")
	}
	if ready && submitter == nil {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "contract_violation", "Declared function capability has no implementation.")
	}
	if submitter == nil {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "not_pending", "function call is no longer pending")
	}
	if err := submitter.SubmitFunctionResult(ctx, result); err != nil {
		code := "runtime_error"
		if errors.Is(err, agent.ErrUnsupportedOperation) {
			code = "contract_violation"
		}
		if errors.Is(err, agent.ErrUnknownFunctionCall) {
			code = "not_pending"
		}
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, code, "function result was not applied")
	}
	r.rememberAppliedFunctionResult(key, fingerprint)
	return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, true, "", "")
}

func (r *Router) rememberAppliedFunctionResult(key string, fingerprint [32]byte) {
	now := time.Now().UTC()
	r.mu.Lock()
	if len(r.applied) >= 1024 {
		cutoff := now.Add(-time.Hour)
		for id, entry := range r.applied {
			if entry.recordedAt.Before(cutoff) {
				delete(r.applied, id)
			}
		}
	}
	if len(r.applied) >= 1024 {
		for id := range r.applied {
			delete(r.applied, id)
			break
		}
	}
	r.applied[key] = appliedFunctionResult{fingerprint: fingerprint, recordedAt: now}
	r.mu.Unlock()
}

func (r *Router) sendInteractionDecisionAck(ctx context.Context, requestID, deliveryID string, applied bool, errorCode, message string) error {
	env, err := proto.NewEnvelope(proto.TypeInteractionDecisionAck, requestID, proto.InteractionDecisionAckPayload{
		DeliveryID: deliveryID,
		Applied:    applied,
		ErrorCode:  errorCode,
		Error:      message,
	})
	if err != nil {
		return fmt.Errorf("dispatch: build interaction decision ack: %w", err)
	}
	if err := r.sender.Send(ctx, env); err != nil {
		return fmt.Errorf("dispatch: send interaction decision ack: %w", err)
	}
	return nil
}
