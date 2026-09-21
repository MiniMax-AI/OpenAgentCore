package mcode

import (
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) projectSubagentMessages(session nativeSubagentSession, turn nativeSubagentTurn) error {
	position := int32(0)
	emit := func(id, kind string, value any) error {
		if id == "" {
			return fmt.Errorf("mcode: native child Item identity is missing")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		s.emit(proto.TypeSubagentItem, proto.SubagentItemPayload{NativeID: session.ID, TurnID: turn.ID, ItemID: id, Position: position, Kind: kind, Payload: raw})
		position++
		return nil
	}
	for _, message := range session.Messages {
		if message.TurnID != turn.ID {
			continue
		}
		if message.Role == "user" {
			if err := emit(message.ID, "message", map[string]string{"text": message.Data.Text}); err != nil {
				return err
			}
			continue
		}
		if message.Role != "assistant" {
			continue
		}
		if message.Data.Thinking != "" {
			if err := emit(message.ID+":reasoning", "reasoning", map[string]any{"status": "completed", "summary": []map[string]string{{"type": "summary_text", "text": message.Data.Thinking}}}); err != nil {
				return err
			}
		}
		if message.Data.Text != "" {
			text := message.Data.Text
			if err := emit(message.ID, proto.TypeOutputMessage, proto.OutputMessagePayload{ID: message.ID, Status: "completed", Text: &text}); err != nil {
				return err
			}
		}
		for _, tool := range message.Data.Tools {
			if tool.Status != 2 && tool.Status != 3 {
				return fmt.Errorf("mcode: child tool did not settle")
			}
			coordination, err := nativeCoordination(session, tool)
			if err != nil {
				return err
			}
			if coordination != nil {
				if err := emit(tool.ID, proto.TypeSubagentCoordination, coordination); err != nil {
					return err
				}
				continue
			}
			var args map[string]any
			var result any
			if tool.Args != "" && json.Unmarshal([]byte(tool.Args), &args) != nil {
				return fmt.Errorf("mcode: invalid child tool input")
			}
			if tool.Result != "" && json.Unmarshal([]byte(tool.Result), &result) != nil {
				result = tool.Result
			}
			status := "completed"
			if tool.Status == 3 {
				status = "failed"
			}
			update := toolUpdate{ID: tool.ID, Name: tool.Name, Status: status, RawInput: args, RawOutput: result}
			observation := workspaceToolObservation(update, "after")
			if observation == nil {
				var err error
				update.mcp, err = s.environmentMCPIdentity(update.Name)
				if err != nil {
					return err
				}
				observation, err = environmentMCPObservation(update, "after")
				if err != nil {
					return err
				}
			}
			// Native task inspection is bookkeeping, not an executed public tool.
			if observation == nil {
				continue
			}
			if err := emit(tool.ID, proto.TypeToolCall, proto.ToolCallPayload{ID: tool.ID, Name: tool.Name, Stage: "after", Args: args, Observation: observation}); err != nil {
				return err
			}
		}
	}
	return nil
}
