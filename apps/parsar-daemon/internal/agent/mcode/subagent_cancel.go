package mcode

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Session) cancelSubagents(ctx context.Context) error {
	select {
	case <-s.finished:
		return nil
	default:
	}
	s.mu.Lock()
	native := s.sessionID
	s.mu.Unlock()
	if native == "" {
		return fmt.Errorf("mcode: native child owner is not ready")
	}
	id, response := s.reserveResponse()
	defer s.removeResponse(id)
	raw, _ := json.Marshal(map[string]string{"sessionId": native})
	if err := s.write(rpcFrame{JSONRPC: "2.0", ID: json.RawMessage(id), Method: "mcode/session/delegation/stop", Params: raw}); err != nil {
		return err
	}
	select {
	case frame := <-response:
		var result struct {
			Receipt *struct {
				Failed []string `json:"failedSessionIds"`
			} `json:"receipt"`
		}
		// An abort acknowledgement is followed by durable terminal verification.
		if frame.Error != nil || json.Unmarshal(frame.Result, &result) != nil || result.Receipt == nil || len(result.Receipt.Failed) != 0 {
			return fmt.Errorf("mcode: child cancellation was not acknowledged")
		}
	case <-s.exited:
		return fmt.Errorf("mcode: native child owner exited during cancellation")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-s.finished:
		s.mu.Lock()
		err := s.subagentSettlementError
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
