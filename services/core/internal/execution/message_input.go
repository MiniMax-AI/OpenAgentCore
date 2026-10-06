package execution

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func messageInput(raw json.RawMessage) (proto.MessageInput, error) {
	var input struct {
		Text  *string           `json:"text"`
		Input []v1.InputMessage `json:"input"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, sessions.ErrInvalidInput
	}
	var messages proto.MessageInput
	if len(input.Input) == 0 && input.Text != nil {
		messages = proto.TextInput(*input.Text)
	}
	for _, message := range input.Input {
		if message.Role != "user" {
			return nil, sessions.ErrInvalidInput
		}
		converted := proto.InputMessage{}
		for _, part := range message.Content {
			converted.Content = append(converted.Content, proto.InputContent{Type: part.Type, Text: part.Text, ImageURL: part.ImageURL})
		}
		messages = append(messages, converted)
	}
	if messages.Validate() != nil {
		return nil, sessions.ErrInvalidInput
	}
	return messages, nil
}

func (d *Dispatcher) initialInput(ctx context.Context, tenant, session, turn string) (proto.MessageInput, int64, error) {
	inputs, err := d.Store.ListTurnInputs(ctx, tenant, session, turn, 0, 100)
	if err != nil {
		return nil, 0, err
	}
	var messages proto.MessageInput
	var through int64
	size := 0
	for _, input := range inputs {
		if input.Kind != "message" {
			return nil, 0, sessions.ErrInvalidInput
		}
		batch, err := messageInput(input.Payload)
		if err != nil {
			return nil, 0, err
		}
		if size+len(input.Payload) > 512*1024 && len(messages) > 0 {
			break
		}
		messages = append(messages, batch...)
		size += len(input.Payload)
		through = input.Sequence
	}
	if len(messages) == 0 {
		return nil, 0, sessions.ErrInvalidInput
	}
	return messages, through, nil
}
