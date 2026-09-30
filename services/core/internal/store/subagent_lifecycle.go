package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func validNativeIdentity(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
func recordSubagentChange(ctx context.Context, q *sqlc.Queries, session, id pgtype.UUID, kind string) error {
	value, err := publicSubagent(ctx, q, session, uuid.UUID(id.Bytes).String())
	if err != nil {
		return err
	}
	return sessionpg.AppendChanges(ctx, q, session, sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.subagent." + kind, Subagent: &value}})
}
func publishSubagent(ctx context.Context, q *sqlc.Queries, session, id pgtype.UUID, identity proto.SubagentIdentityPayload) error {
	previous, err := q.GetNativeSubagent(ctx, sqlc.GetNativeSubagentParams{SessionID: session, NativeID: identity.NativeID})
	if err != nil {
		return err
	}
	// Identity metadata is immutable after its first public observation.
	if previous.PublicVisible {
		return nil
	}
	p := sqlc.PublishSubagentParams{SessionID: session, ID: id}
	if identity.Name != nil {
		p.Name = pgtype.Text{String: *identity.Name, Valid: true}
	}
	if identity.Instructions != nil {
		p.Instructions = pgtype.Text{String: *identity.Instructions, Valid: true}
	}
	if err := q.PublishSubagent(ctx, p); err != nil {
		return err
	}
	return recordSubagentChange(ctx, q, session, id, "created")
}
func projectSubagentLifecycle(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, raw json.RawMessage) error {
	var p proto.SubagentLifecyclePayload
	if json.Unmarshal(raw, &p) != nil || !validNativeIdentity(p.NativeID) || !validNativeIdentity(p.EffectID) || p.OccurredAtMS <= 0 || (p.Status != "active" && p.Status != "closed") {
		return sessions.ErrInvalidInput
	}
	child, err := q.GetNativeSubagent(ctx, sqlc.GetNativeSubagentParams{SessionID: session, NativeID: p.NativeID})
	if err != nil {
		return err
	}
	if !child.PublicVisible || p.OccurredAtMS < child.NativeCreatedAt*1000 {
		return sessions.ErrInvalidInput
	}
	inserted, err := q.PutSubagentEffect(ctx, sqlc.PutSubagentEffectParams{SessionID: session, EffectID: p.EffectID, Payload: raw})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrIdempotencyConflict
	}
	if err != nil || !inserted {
		return err
	}
	if p.OccurredAtMS < child.LifecycleAtMs {
		return sessions.ErrIdempotencyConflict
	}
	if p.Status == child.Status {
		return nil
	}
	closed := pgtype.Int8{}
	if p.Status == "closed" {
		closed = pgtype.Int8{Int64: p.OccurredAtMS, Valid: true}
	}
	if err = q.ApplySubagentLifecycle(ctx, sqlc.ApplySubagentLifecycleParams{SessionID: session, ID: child.ID, Status: p.Status, ClosedAtMs: closed, LifecycleAtMs: p.OccurredAtMS}); err != nil {
		return err
	}
	return recordSubagentChange(ctx, q, session, child.ID, p.Status)
}
