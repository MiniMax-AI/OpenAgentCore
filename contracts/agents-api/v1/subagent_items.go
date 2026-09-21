package v1

import (
	"encoding/json"
	"errors"
)

type CreateSubagentCallItem struct {
	ID              string         `json:"id" binding:"required"`
	TurnID          string         `json:"turn_id" binding:"required"`
	Type            string         `json:"type" binding:"required" enums:"create_subagent_call"`
	Status          string         `json:"status" binding:"required" enums:"in_progress,completed,failed,incomplete"`
	AgentID         string         `json:"agent_id" binding:"required"`
	Content         []AgentContent `json:"content" binding:"required"`
	Model           *string        `json:"model" extensions:"x-nullable"`
	ReasoningEffort *string        `json:"reasoning_effort" extensions:"x-nullable"`
}

type SendSubagentInputCallItem struct {
	ID               string         `json:"id" binding:"required"`
	TurnID           string         `json:"turn_id" binding:"required"`
	Type             string         `json:"type" binding:"required" enums:"send_subagent_input_call"`
	Status           string         `json:"status" binding:"required" enums:"in_progress,completed,failed,incomplete"`
	SenderAgentID    string         `json:"sender_agent_id" binding:"required"`
	RecipientAgentID string         `json:"recipient_agent_id" binding:"required"`
	Content          []AgentContent `json:"content" binding:"required"`
}

// SubagentControlCallItem is the common wire shape of resume, interrupt and close calls.
type SubagentControlCallItem struct {
	ID               string `json:"id" binding:"required"`
	TurnID           string `json:"turn_id" binding:"required"`
	Type             string `json:"type" binding:"required" enums:"resume_subagent_call,interrupt_subagent_call,close_subagent_call"`
	Status           string `json:"status" binding:"required" enums:"in_progress,completed,failed,incomplete"`
	SenderAgentID    string `json:"sender_agent_id" binding:"required"`
	RecipientAgentID string `json:"recipient_agent_id" binding:"required"`
}

type WaitForSubagentsCallItem struct {
	ID                string   `json:"id" binding:"required"`
	TurnID            string   `json:"turn_id" binding:"required"`
	Type              string   `json:"type" binding:"required" enums:"wait_for_subagents_call"`
	Status            string   `json:"status" binding:"required" enums:"in_progress,completed,failed,incomplete"`
	SenderAgentID     string   `json:"sender_agent_id" binding:"required"`
	RecipientAgentIDs []string `json:"recipient_agent_ids" binding:"required"`
}

type AgentMessageItem struct {
	ID               string         `json:"id" binding:"required"`
	TurnID           string         `json:"turn_id" binding:"required"`
	Type             string         `json:"type" binding:"required" enums:"agent_message"`
	SenderAgentID    string         `json:"sender_agent_id" binding:"required"`
	RecipientAgentID string         `json:"recipient_agent_id" binding:"required"`
	Content          []AgentContent `json:"content" binding:"required"`
}

type SummaryText struct {
	Type string `json:"type" binding:"required" enums:"summary_text"`
	Text string `json:"text" binding:"required"`
}

type ReasoningItem struct {
	ID      string        `json:"id" binding:"required"`
	TurnID  string        `json:"turn_id" binding:"required"`
	Type    string        `json:"type" binding:"required" enums:"reasoning"`
	Status  *string       `json:"status" extensions:"x-nullable" enums:"in_progress,completed,incomplete"`
	Summary []SummaryText `json:"summary" binding:"required"`
}

// MarshalJSON preserves each coordination variant's required fields and nulls.
// Existing execution Item variants retain their established serialization.
func (i Item) MarshalJSON() ([]byte, error) {
	switch i.Type {
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
	default:
		type wire Item
		return json.Marshal(wire(i))
	}
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
