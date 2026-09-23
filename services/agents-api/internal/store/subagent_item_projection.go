package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func isSubagentObservation(kind string) bool {
	switch kind {
	case proto.TypeSubagentIdentity, proto.TypeSubagentLifecycle, proto.TypeSubagentTurn, proto.TypeSubagentItem, proto.TypeSubagentCoordination:
		return true
	}
	return false
}
func projectSubagentItem(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, raw json.RawMessage) error {
	var p proto.SubagentItemPayload
	if json.Unmarshal(raw, &p) != nil || !validNativeIdentity(p.NativeID) || !validNativeIdentity(p.TurnID) || !validNativeIdentity(p.ItemID) || p.Position < 0 || p.Position > 1073741823 {
		return ErrInvalidInput
	}
	child, err := q.GetNativeSubagent(ctx, sqlc.GetNativeSubagentParams{SessionID: session, NativeID: p.NativeID})
	if err != nil {
		return err
	}
	if !child.PublicVisible {
		return ErrInvalidInput
	}
	turnID := items.Identity(uuid.UUID(child.ID.Bytes).String(), "turn:"+p.TurnID)
	turn, err := childTurn(ctx, q, session, uuid.UUID(child.ID.Bytes).String(), turnID)
	if err != nil {
		return err
	}
	projected, err := childItems(ctx, q, session, turnID, p)
	if err != nil {
		return err
	}
	for offset, item := range projected {
		if err := putChildItem(ctx, q, session, child.ID, turn, p.Position*2+int32(offset), item); err != nil {
			return err
		}
	}
	return nil
}

func putChildItem(ctx context.Context, q *sqlc.Queries, session, childID pgtype.UUID, turn sqlc.SubagentTurn, position int32, item v1.Item) error {
	payload, err := item.MarshalStored()
	if err != nil {
		return err
	}
	id, _ := parseID(item.ID)
	old, err := q.GetChildItem(ctx, sqlc.GetChildItemParams{SessionID: session, SubagentID: childID, ID: id, Candidate: payload})
	fresh := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !fresh {
		return err
	}
	var previous v1.Item
	if !fresh {
		if old.TurnID != turn.ID || old.Position != position {
			return ErrIdempotencyConflict
		}
		if err = json.Unmarshal(old.Payload, &previous); err != nil {
			return err
		}
		if old.PayloadEqual {
			return nil
		}
		if previous.Status != "in_progress" {
			return ErrIdempotencyConflict
		}
	}
	if terminalStatus(turn.Status) {
		return ErrTurnConflict
	}
	// Child Items publish no Session events: the Session stream carries root work,
	// and child history is read through the Subagent routes. The stored output
	// index keeps its existing meaning.
	_, err = q.PutChildItem(ctx, sqlc.PutChildItemParams{ID: id, SessionID: session, SubagentID: childID, TurnID: turn.ID, Position: position, Payload: payload, IsOutput: item.Role != "user" && item.Type != "function_call_output"})
	return err
}

func childItems(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, turn string, p proto.SubagentItemPayload) ([]v1.Item, error) {
	var result []v1.Item
	switch p.Kind {
	case proto.TypeSubagentCoordination:
		var value proto.SubagentCoordinationPayload
		if json.Unmarshal(p.Payload, &value) != nil || value.ID != p.ItemID || value.ActorID != p.NativeID {
			return result, ErrInvalidInput
		}
		item, err := coordinationItem(ctx, q, session, turn, value)
		return []v1.Item{item}, err
	case proto.TypeOutputMessage:
		var message proto.OutputMessagePayload
		if json.Unmarshal(p.Payload, &message) != nil || message.ID != p.ItemID || message.Text == nil {
			return result, ErrInvalidInput
		}
	case proto.TypeToolCall:
		var call proto.ToolCallPayload
		if json.Unmarshal(p.Payload, &call) != nil || call.ID != p.ItemID || len(call.NativeItem) > 0 || call.Observation == nil {
			return result, ErrInvalidInput
		}
	case "message":
		// Input projection validates and normalizes the existing public message shape.
	case "reasoning":
		var value struct {
			Status  string           `json:"status"`
			Summary []v1.SummaryText `json:"summary"`
		}
		if json.Unmarshal(p.Payload, &value) != nil {
			return result, ErrInvalidInput
		}
		if value.Status != "" && value.Status != "in_progress" && value.Status != "completed" && value.Status != "incomplete" {
			return result, ErrInvalidInput
		}
		for _, part := range value.Summary {
			if part.Type != "summary_text" {
				return result, ErrInvalidInput
			}
		}
		return []v1.Item{{ID: items.Identity(turn, "reasoning:"+p.ItemID), TurnID: turn, Type: "reasoning", Status: value.Status, Summary: value.Summary}}, nil
	default:
		return result, ErrInvalidInput
	}
	updates, err := items.Project(turn, p.Kind, int64(p.Position), p.Payload)
	if err != nil {
		return result, err
	}
	if len(updates) < 1 || len(updates) > 2 {
		return nil, ErrInvalidInput
	}
	for _, update := range updates {
		if update.AppendText {
			return nil, ErrInvalidInput
		}
		result = append(result, update.Item)
	}
	return result, nil
}
