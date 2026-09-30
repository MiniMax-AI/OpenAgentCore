package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func subagentCoordination(raw json.RawMessage, actor string) (*proto.SubagentCoordinationPayload, error) {
	var item struct {
		subagentCollaboration
		Model           *string `json:"model"`
		ReasoningEffort *string `json:"reasoningEffort"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return nil, errors.New("codex: invalid subagent coordination Item")
	}
	if item.Type != "collabAgentToolCall" {
		return nil, nil
	}
	kinds := map[string]string{"spawnAgent": "create_subagent_call", "sendInput": "send_subagent_input_call", "wait": "wait_for_subagents_call", "closeAgent": "close_subagent_call", "resumeAgent": "resume_subagent_call"}
	kind, ok := kinds[item.Tool]
	if !ok {
		return nil, errors.New("codex: unsupported subagent coordination tool")
	}
	status := observationStatus(item.Status, "after")
	if item.Status == "inProgress" {
		status = "in_progress"
	}
	return &proto.SubagentCoordinationPayload{ID: item.ID, Kind: kind, Status: status, ActorID: actor, Recipients: item.Receivers, Text: item.Prompt, Model: item.Model, ReasoningEffort: item.ReasoningEffort}, nil
}

func (s *Session) publishSubagentCoordination(ctx context.Context, h subagentHistory, known map[string]bool) error {
	root := s.currentThreadID()

	s.steering.mu.Lock()
	rootTurn := s.steering.id
	s.steering.mu.Unlock()
	for _, turn := range h.Turns {
		if h.ID == root && turn.ID != rootTurn {
			continue
		}
		for _, raw := range turn.Items {
			payload, err := subagentCoordination(raw, "")
			if err != nil {
				return err
			}
			if payload == nil {
				continue
			}
			for _, id := range payload.Recipients {
				if (id == root || !known[id]) && payload.Status != "failed" {
					return errors.New("codex: coordination recipient has no verified subagent identity")
				}
			}
			if err := s.sendSubagentFact(ctx, proto.TypeSubagentCoordination, turn.ID+":"+payload.ID, payload); err != nil {
				return err
			}
		}
	}
	return nil
}
