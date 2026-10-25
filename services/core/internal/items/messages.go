package items

import (
	"encoding/json"
	"errors"
	"slices"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

type Update struct {
	Item               v1.Item
	AppendText         bool
	CommandOutputDelta *string
	// SummaryIndex is the reasoning summary part an appended fragment extends.
	SummaryIndex int
	// An aggregate answer is used only when no native message identity was recorded.
	AggregateFinal bool
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

func Project(turn, kind string, sequence int64, raw json.RawMessage) ([]Update, error) {
	switch kind {
	case "message":
		return inputMessages(turn, sequence, raw), nil
	case proto.TypeDelta:
		var p proto.DeltaPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		key := "message:" + p.ItemID
		if p.ItemID == "" {
			key = "aggregate-message"
		}
		return []Update{{Item: message(turn, key, "assistant", p.Delta, "in_progress"), AppendText: true}}, nil
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
	case proto.TypeReasoning:
		var p proto.ReasoningPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !p.Valid() {
			return nil, errors.New("invalid reasoning observation")
		}
		item := v1.Item{ID: Identity(turn, "reasoning:"+p.ID), TurnID: turn, Type: "reasoning", Status: p.Status}
		if p.Delta != "" {
			item.Summary = []v1.SummaryText{{Type: "summary_text", Text: p.Delta}}
		}
		return []Update{{Item: item, AppendText: true, SummaryIndex: p.Index}}, nil
	case proto.TypeDone, "execution_failed":
		// Done may contain adapter diagnostics. Only a successful Turn
		// confirms aggregate answer text; failures retain observed message deltas.
		return nil, nil
	case "cancel_receipt", "execution_completed", "execution_cancelled":
		var p struct {
			Applied bool               `json:"applied"`
			Outcome *proto.DonePayload `json:"outcome"`
			Done    *proto.DonePayload `json:"done"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		final := p.Done
		if kind == "cancel_receipt" {
			if !p.Applied {
				return nil, nil
			}
			final = p.Outcome
		}
		if final == nil || final.Content == "" {
			return nil, nil
		}
		status := "incomplete"
		if kind == "execution_completed" {
			status = "completed"
		}
		return []Update{{Item: message(turn, "aggregate-message", "assistant", final.Content, status), AggregateFinal: true}}, nil
	case proto.TypeToolCall:
		return projectTool(turn, raw)
	case proto.TypeCommandOutput:
		return projectCommandOutput(turn, raw)
	default:
		return nil, nil
	}
}

// Reasoning parts advance in order: a fragment extends the current part or
// starts the next one.
var errReasoningPart = errors.New("reasoning part out of order")

func Merge(update Update, previous v1.Item) (v1.Item, error) {
	if update.CommandOutputDelta != nil {
		return mergeCommandOutput(update, previous)
	}
	item := update.Item
	if previous.ID == "" {
		if update.SummaryIndex != 0 {
			return previous, errReasoningPart
		}
		return item, nil
	}
	if previous.Status != "in_progress" && !(update.AggregateFinal && previous.Status == "incomplete") {
		return previous, nil
	}
	if (item.Type == "function_call") && (item.Arguments == nil || string(encoded(item.Arguments)) == "null") {
		item.Arguments = previous.Arguments
	}
	if item.Type == "command_execution" && item.Output == nil {
		item.Output = previous.Output
	}
	if update.AppendText {
		if item.Type == "reasoning" {
			parts, index := slices.Clone(previous.Summary), update.SummaryIndex
			switch {
			case len(item.Summary) == 0:
			case index == len(parts):
				parts = append(parts, item.Summary[0])
			case index == len(parts)-1:
				parts[index].Text += item.Summary[0].Text
			default:
				return previous, errReasoningPart
			}
			item.Summary = parts
			return item, nil
		}
		text := *previous.Content[0].Text + *item.Content[0].Text
		item.Content = slices.Clone(item.Content)
		item.Content[0].Text = &text
		if item.Phase == "" {
			item.Phase = previous.Phase
		}
	}
	return item, nil
}
