package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var _ agent.DurableSteerer = (*Session)(nil)

func (s *Session) Steer(ctx context.Context, input proto.PromptSteerPayload) error {
	return s.SteerWithReceipt(ctx, input, nil)
}

// Native acceptance belongs to the active ACP Turn; it does not promise model consumption.
func (s *Session) SteerWithReceipt(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
	if s.executor != nil {
		s.mu.Lock()
		if s.closing || s.cancelled {
			s.mu.Unlock()
			return agent.ErrSteeringInactive
		}
		s.operations.Add(1)
		s.mu.Unlock()
		defer s.operations.Done()
	}
	text, err := input.Input.TextOnly()
	if strings.TrimSpace(input.InputID) == "" || err != nil {
		return agent.ErrSteeringRejected
	}
	s.mu.Lock()
	ready, native := s.steeringReady, s.sessionID
	s.mu.Unlock()
	if s.process.Context().Err() != nil {
		return agent.ErrSteeringInactive
	}
	if !ready || native == "" {
		return agent.ErrSteeringNotReady
	}
	id, response := s.reserveResponse()
	defer s.removeResponse(id)
	raw, err := json.Marshal(map[string]string{"sessionId": native, "text": text, "clientRequestId": input.InputID})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = s.writeContext(ctx, rpcFrame{JSONRPC: "2.0", ID: json.RawMessage(id), Method: "mcode/session/steer", Params: raw})
	if err != nil {
		s.markInputUncertain()
		return fmt.Errorf("mcode: input transport failed")
	}
	if written != nil {
		written()
	}
	var frame rpcFrame
	var stopped error
	select {
	case frame = <-response:
	case <-s.inputSettlementDone():
		stopped = fmt.Errorf("mcode: run ended with unknown input outcome")
	case <-s.exited:
		stopped = fmt.Errorf("mcode: input transport closed")
	case <-ctx.Done():
		stopped = ctx.Err()
	}
	if stopped != nil {
		// A native receipt already read before terminal closure remains authoritative.
		select {
		case frame = <-response:
		default:
			s.markInputUncertain()
			return stopped
		}
	}
	if frame.Error != nil {
		if frame.Error.Code == -32602 || frame.Error.Code == -32601 {
			return agent.ErrSteeringRejected
		}
		s.markInputUncertain()
		return fmt.Errorf("mcode: native steering failed with unknown input outcome")
	}
	var result struct {
		TurnID string `json:"turnId"`
		Mode   string `json:"mode"`
	}
	if json.Unmarshal(frame.Result, &result) != nil || result.TurnID == "" || (result.Mode != "steered" && result.Mode != "duplicate") {
		s.markInputUncertain()
		return fmt.Errorf("mcode: invalid steering receipt")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.steeringTurn != "" && s.steeringTurn != result.TurnID {
		s.inputUncertain = true
		return fmt.Errorf("mcode: steering turn changed")
	}
	s.steeringTurn = result.TurnID
	return nil
}

func (s *Session) inputSettlementDone() <-chan struct{} {
	if s.inputDone != nil {
		return s.inputDone
	}
	return s.finished
}

func (s *Session) CancellationOutcome() proto.DonePayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcome.Metadata != nil {
		return s.outcome
	}
	metadata := map[string]any{proto.DoneMetaAgentSessionType: "mcode"}
	if s.sessionID != "" {
		metadata[proto.DoneMetaAgentSessionID] = s.sessionID
	}
	return proto.DonePayload{Metadata: metadata}
}

func (s *Session) markInputUncertain() {
	s.mu.Lock()
	s.inputUncertain = true
	s.mu.Unlock()
}
