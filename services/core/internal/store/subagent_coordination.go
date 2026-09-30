package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func coordinationItem(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, turn string, p proto.SubagentCoordinationPayload) (v1.Item, error) {
	value := v1.Item{ID: items.Identity(turn, "coordination:"+p.ID), TurnID: turn, Type: p.Kind, Status: p.Status, Model: p.Model, ReasoningEffort: p.ReasoningEffort}
	if !validNativeIdentity(p.ID) {
		return value, sessions.ErrInvalidInput
	}
	if p.Status != "in_progress" && p.Status != "completed" && p.Status != "failed" && p.Status != "incomplete" && p.Kind != "agent_message" {
		return value, sessions.ErrInvalidInput
	}
	resolve := func(native string, failedReference bool) (string, error) {
		if native == "" {
			return q.SubagentRootAgent(ctx, session)
		}
		row, err := q.GetNativeSubagent(ctx, sqlc.GetNativeSubagentParams{SessionID: session, NativeID: native})
		if errors.Is(err, pgx.ErrNoRows) && failedReference && validNativeIdentity(native) {
			return native, nil
		}
		if err != nil {
			return "", err
		}
		if !row.PublicVisible {
			return "", sessions.ErrNotFound
		}
		return uuid.UUID(row.ID.Bytes).String(), nil
	}
	actor, err := resolve(p.ActorID, false)
	if err != nil {
		return value, err
	}
	if actor == "" {
		return value, sessions.ErrInvalidInput
	}
	value.SenderAgentID = actor
	if p.Text != nil {
		value.Content = []v1.ItemContent{{Type: "output_text", Text: p.Text}}
	}
	switch p.Kind {
	case "create_subagent_call":
		value.AgentID = actor
	case "wait_for_subagents_call":
		value.RecipientAgentIDs = []string{}
		for _, recipient := range p.Recipients {
			id, err := resolve(recipient, p.Status == "failed")
			if err != nil {
				return value, err
			}
			value.RecipientAgentIDs = append(value.RecipientAgentIDs, id)
		}
	case "send_subagent_input_call", "resume_subagent_call", "interrupt_subagent_call", "close_subagent_call", "agent_message":
		if len(p.Recipients) != 1 {
			return value, sessions.ErrInvalidInput
		}
		value.RecipientAgentID, err = resolve(p.Recipients[0], p.Status == "failed")
		if err != nil {
			return value, err
		}
	default:
		return value, sessions.ErrInvalidInput
	}
	return value, nil
}
func projectRootCoordination(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, raw json.RawMessage, created pgtype.Timestamptz) error {
	var p proto.SubagentCoordinationPayload
	if json.Unmarshal(raw, &p) != nil || p.ActorID != "" {
		return sessions.ErrInvalidInput
	}
	value, err := coordinationItem(ctx, q, session, uuid.UUID(turn.Bytes).String(), p)
	if err != nil {
		return err
	}
	update := items.Update{Item: value}
	stored, err := sessionpg.LoadItem(ctx, q, session, turn, update)
	if err != nil {
		return err
	}
	change, ok, err := items.Observe(proto.TypeSubagentCoordination, update, stored)
	if err != nil || !ok {
		return err
	}
	index, err := sessionpg.PutItem(ctx, q, session, turn, created, change)
	if err != nil {
		return err
	}
	return sessionpg.AppendChanges(ctx, q, session, sessions.ItemChanges(change, index)...)
}
