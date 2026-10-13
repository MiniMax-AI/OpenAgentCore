package v1

import (
	"encoding/json"
	"errors"
)

// MarshalJSON renders the wire shape, which is also the stored encoding.
// Messages always carry content and a nullable phase, function results a
// nullable output and error, commands a nullable cwd, output, exit code and
// duration, and web search a nullable action. Coordination and reasoning
// variants keep their required fields and nulls.
func (i Item) MarshalJSON() ([]byte, error) {
	type wire Item
	switch i.Type {
	case "message":
		var phase *string
		if i.Phase != "" {
			phase = &i.Phase
		}
		content := i.Content
		if content == nil {
			content = []ItemContent{}
		}
		return json.Marshal(struct {
			wire
			Phase   *string       `json:"phase"`
			Content []ItemContent `json:"content"`
		}{wire(i), phase, content})
	case "command_execution":
		return json.Marshal(struct {
			wire
			Cwd        *string `json:"cwd"`
			Output     any     `json:"output"`
			ExitCode   *int64  `json:"exit_code"`
			DurationMS *int64  `json:"duration_ms"`
		}{wire(i), i.Cwd, i.Output, i.ExitCode, i.DurationMS})
	case "web_search_call":
		return json.Marshal(struct {
			wire
			Action *WebSearchAction `json:"action"`
		}{wire(i), i.Action})
	case "function_call_output":
		return json.Marshal(struct {
			wire
			Output any `json:"output"`
			Error  any `json:"error"`
		}{wire(i), i.Output, i.Error})
	case "create_subagent_call", "send_subagent_input_call", "agent_message":
		content, err := coordinationContent(i.Content)
		if err != nil {
			return nil, err
		}
		switch i.Type {
		case "create_subagent_call":
			return json.Marshal(CreateSubagentCallItem{ID: i.ID, TurnID: i.TurnID, Type: i.Type, Status: i.Status, AgentID: i.AgentID, Content: content, Model: i.Model, ReasoningEffort: i.ReasoningEffort})
		case "send_subagent_input_call":
			return json.Marshal(SendSubagentInputCallItem{ID: i.ID, TurnID: i.TurnID, Type: i.Type, Status: i.Status, SenderAgentID: i.SenderAgentID, RecipientAgentID: i.RecipientAgentID, Content: content})
		default:
			return json.Marshal(AgentMessageItem{ID: i.ID, TurnID: i.TurnID, Type: i.Type, SenderAgentID: i.SenderAgentID, RecipientAgentID: i.RecipientAgentID, Content: content})
		}
	case "resume_subagent_call", "interrupt_subagent_call", "close_subagent_call":
		return json.Marshal(SubagentControlCallItem{ID: i.ID, TurnID: i.TurnID, Type: i.Type, Status: i.Status, SenderAgentID: i.SenderAgentID, RecipientAgentID: i.RecipientAgentID})
	case "wait_for_subagents_call":
		recipients := i.RecipientAgentIDs
		if recipients == nil {
			recipients = []string{}
		}
		return json.Marshal(WaitForSubagentsCallItem{ID: i.ID, TurnID: i.TurnID, Type: i.Type, Status: i.Status, SenderAgentID: i.SenderAgentID, RecipientAgentIDs: recipients})
	case "reasoning":
		var status *string
		if i.Status != "" {
			status = &i.Status
		}
		summary := i.Summary
		if summary == nil {
			summary = []SummaryText{}
		}
		return json.Marshal(ReasoningItem{ID: i.ID, TurnID: i.TurnID, Type: i.Type, Status: status, Summary: summary})
	}
	return json.Marshal(wire(i))
}

func coordinationContent(parts []ItemContent) ([]AgentContent, error) {
	result := make([]AgentContent, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "output_text":
			if part.Text == nil {
				return nil, errors.New("missing inter-agent text")
			}
			result = append(result, AgentContent{Type: part.Type, Text: part.Text})
		case "encrypted_content":
			if part.EncryptedContent == nil {
				return nil, errors.New("missing encrypted inter-agent content")
			}
			result = append(result, AgentContent{Type: part.Type, EncryptedContent: part.EncryptedContent})
		default:
			return nil, errors.New("unsupported inter-agent content type")
		}
	}
	return result, nil
}
