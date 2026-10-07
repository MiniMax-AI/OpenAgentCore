package items

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

type Update struct {
	Item               v1.Item
	AppendText         bool
	CommandOutputDelta *string
}

func Identity(turn, key string) string {
	return uuid.NewSHA1(uuid.MustParse(turn), []byte(key)).String()
}

func message(turn, key, role, text, status string) v1.Item {
	contentType := "output_text"
	if role == "user" {
		contentType = "input_text"
	}
	return v1.Item{ID: Identity(turn, key), TurnID: turn, Type: "message", Role: role, Status: status,
		Content: []v1.ItemContent{{Type: contentType, Text: &text}}}
}

// ErrInvalidObservation marks an execution observation that projects to no
// valid Item: the Runtime sent it, so it is never a storage failure.
var ErrInvalidObservation = errors.New("invalid execution observation")

// Project returns the Item updates an observation of kind makes. Every error
// wraps ErrInvalidObservation.
func Project(turn, kind string, sequence int64, raw json.RawMessage) (updates []Update, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrInvalidObservation, err)
		}
	}()
	switch kind {
	case "message":
		return inputMessages(turn, sequence, raw), nil
	case proto.TypeDelta:
		var p proto.DeltaPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		return []Update{{Item: message(turn, "message:"+p.ItemID, "assistant", p.Delta, "in_progress"), AppendText: true}}, nil
	case proto.TypeOutputMessage:
		var p proto.OutputMessagePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if p.ID == "" || (p.Status != "in_progress" && p.Status != "completed" && p.Status != "incomplete") {
			return nil, errors.New("invalid message observation")
		}
		text := ""
		if p.Text != nil {
			text = *p.Text
		}
		item := message(turn, "message:"+p.ID, "assistant", text, p.Status)
		if p.Phase == "commentary" || p.Phase == "final_answer" {
			item.Phase = p.Phase
		}
		return []Update{{Item: item, AppendText: p.Text == nil}}, nil
	case proto.TypeToolCall:
		return projectTool(turn, raw)
	case proto.TypeCommandOutput:
		return projectCommandOutput(turn, raw)
	default:
		return nil, nil
	}
}

func Merge(update Update, previous v1.Item) (v1.Item, error) {
	if update.CommandOutputDelta != nil {
		return mergeCommandOutput(update, previous)
	}
	item := update.Item
	if previous.ID == "" {
		return item, nil
	}
	if previous.Status != "in_progress" {
		return previous, nil
	}
	if (item.Type == "function_call") && (item.Arguments == nil || string(encoded(item.Arguments)) == "null") {
		item.Arguments = previous.Arguments
	}
	if item.Type == "command_execution" && item.Output == nil {
		item.Output = previous.Output
	}
	if update.AppendText {
		text := *previous.Content[0].Text + *item.Content[0].Text
		item.Content = slices.Clone(item.Content)
		item.Content[0].Text = &text
		if item.Phase == "" {
			item.Phase = previous.Phase
		}
	}
	return item, nil
}
