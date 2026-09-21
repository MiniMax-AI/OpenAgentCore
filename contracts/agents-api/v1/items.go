package v1

import (
	"bytes"
	"encoding/json"
)

// Item contains the supported variants of the pinned Session Item union.
type Item struct {
	ID                string           `json:"id" binding:"required"`
	TurnID            string           `json:"turn_id" binding:"required"`
	Type              string           `json:"type" binding:"required" enums:"message,command_execution,mcp_call,function_call,function_call_output,web_search_call,reasoning,agent_message,create_subagent_call,send_subagent_input_call,resume_subagent_call,wait_for_subagents_call,interrupt_subagent_call,close_subagent_call"`
	Status            string           `json:"status" enums:"in_progress,completed,failed,incomplete"`
	Role              string           `json:"role,omitempty" enums:"user,assistant"`
	Phase             string           `json:"phase,omitempty" enums:"commentary,final_answer"`
	Content           []ItemContent    `json:"content,omitempty"`
	Command           string           `json:"command,omitempty"`
	Cwd               *string          `json:"cwd,omitempty"`
	DurationMS        *int64           `json:"duration_ms,omitempty"`
	ExitCode          *int64           `json:"exit_code,omitempty"`
	Name              string           `json:"name,omitempty"`
	CallID            string           `json:"call_id,omitempty"`
	ServerLabel       string           `json:"server_label,omitempty"`
	Arguments         any              `json:"arguments,omitempty"`
	Output            any              `json:"output,omitempty"`
	Error             any              `json:"error,omitempty"`
	Action            *WebSearchAction `json:"action,omitempty"`
	AgentID           string           `json:"agent_id,omitempty"`
	SenderAgentID     string           `json:"sender_agent_id,omitempty"`
	RecipientAgentID  string           `json:"recipient_agent_id,omitempty"`
	RecipientAgentIDs []string         `json:"recipient_agent_ids,omitempty"`
	Model             *string          `json:"model,omitempty"`
	ReasoningEffort   *string          `json:"reasoning_effort,omitempty"`
	Summary           []SummaryText    `json:"summary,omitempty"`
}

type ItemContent struct {
	Type             string  `json:"type" binding:"required" enums:"input_text,output_text,input_image,encrypted_content"`
	Text             *string `json:"text,omitempty"`
	ImageURL         string  `json:"image_url,omitempty"`
	EncryptedContent *string `json:"encrypted_content,omitempty"`
}

type WebSearchAction struct {
	Type    string   `json:"type" binding:"required" enums:"search,open_page,find_in_page,other"`
	Query   *string  `json:"query,omitempty"`
	Queries []string `json:"queries,omitempty"`
	URL     *string  `json:"url,omitempty"`
	Pattern *string  `json:"pattern,omitempty"`
}

type ItemList struct {
	Data    []Item `json:"data" binding:"required"`
	HasMore bool   `json:"has_more" binding:"required"`
}

// UnmarshalJSON preserves integer precision in tool arguments and structured results.
func (i *Item) UnmarshalJSON(raw []byte) error {
	type wire Item
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.Type == "function_call_output" {
		var fields struct {
			Output json.RawMessage
			Error  json.RawMessage
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if value.Output == nil && len(fields.Output) > 0 {
			value.Output = fields.Output
		}
		if value.Error == nil && len(fields.Error) > 0 {
			value.Error = fields.Error
		}
	}
	// MCP has required nullable output/error fields.
	if value.Type == "mcp_call" {
		if value.Output == nil {
			value.Output = json.RawMessage(`null`)
		}
		if value.Error == nil {
			value.Error = json.RawMessage(`null`)
		}
	}
	if value.Arguments == nil && (value.Type == "mcp_call" || value.Type == "function_call") {
		value.Arguments = json.RawMessage(`null`)
	}
	*i = Item(value)
	return nil
}
