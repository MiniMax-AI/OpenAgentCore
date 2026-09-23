package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func nativeMillis(value *int64) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.UnixMilli(*value), Valid: true}
}
func childStoreTurn(row sqlc.SubagentTurn) Turn {
	return Turn{ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), SubagentID: uuid.UUID(row.SubagentID.Bytes).String(), Status: row.Status, CreatedAt: row.CreatedAt.Time, StartedAt: row.StartedAt.Time, CompletedAt: row.CompletedAt.Time, Usage: row.TokenUsage}
}
func projectSubagentTurn(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, raw json.RawMessage) error {
	var p proto.SubagentTurnPayload
	if json.Unmarshal(raw, &p) != nil || !validNativeIdentity(p.NativeID) || !validNativeIdentity(p.TurnID) || p.CreatedAtMS <= 0 {
		return ErrInvalidInput
	}
	if p.Status != TurnQueued && p.Status != TurnInProgress && p.Status != TurnWaiting && !terminalStatus(p.Status) {
		return ErrInvalidInput
	}
	if terminalStatus(p.Status) != (p.CompletedAtMS != nil) {
		return ErrInvalidInput
	}
	if (p.StartedAtMS != nil && *p.StartedAtMS < p.CreatedAtMS) || (p.CompletedAtMS != nil && (*p.CompletedAtMS < p.CreatedAtMS || (p.StartedAtMS != nil && *p.CompletedAtMS < *p.StartedAtMS))) {
		return ErrInvalidInput
	}
	child, err := q.GetNativeSubagent(ctx, sqlc.GetNativeSubagentParams{SessionID: session, NativeID: p.NativeID})
	if err != nil {
		return err
	}
	if !child.PublicVisible {
		return ErrInvalidInput
	}
	id, _ := parseID(items.Identity(uuid.UUID(child.ID.Bytes).String(), "turn:"+p.TurnID))
	old, err := q.GetChildTurn(ctx, sqlc.GetChildTurnParams{SessionID: session, ID: id})
	fresh := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !fresh {
		return err
	}
	usage := []byte(nil)
	if p.Usage != nil {
		value, _ := json.Marshal(p.Usage)
		if measured := measuredUsage("usage", value); measured != nil {
			usage, _ = json.Marshal(measured)
		} else {
			return ErrInvalidInput
		}
	}
	// Re-reading native history cannot reopen or mutate a completed child Turn.
	if !fresh && terminalStatus(old.Status) {
		if !terminalStatus(p.Status) {
			return nil
		}
		var previousUsage, nextUsage *v1.TokenUsage
		_ = json.Unmarshal(old.TokenUsage, &previousUsage)
		_ = json.Unmarshal(usage, &nextUsage)
		if old.Status != p.Status || old.CompletedAt.Time.UnixMilli() != *p.CompletedAtMS || !reflect.DeepEqual(previousUsage, nextUsage) {
			return ErrIdempotencyConflict
		}
		return nil
	}
	if !fresh && old.Status != p.Status && !validTransition(old.Status, p.Status) {
		return ErrTurnConflict
	}
	row, err := q.PutChildTurn(ctx, sqlc.PutChildTurnParams{ID: id, SessionID: session, SubagentID: child.ID, NativeID: p.TurnID, Status: p.Status, CreatedAt: pgtype.Timestamptz{Time: time.UnixMilli(p.CreatedAtMS), Valid: true}, StartedAt: nativeMillis(p.StartedAtMS), CompletedAt: nativeMillis(p.CompletedAtMS), TokenUsage: usage})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIdempotencyConflict
	}
	if err != nil {
		return err
	}
	// Terminal replays returned above; they must not restart the managed idle timer.
	if terminalStatus(row.Status) {
		if err := q.RecordRuntimeTerminalActivity(ctx, session); err != nil {
			return err
		}
	}
	value := publicChildTurn(row)
	emit := func(kind string) error {
		return recordSessionChange(ctx, q, session, SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn." + kind, TurnID: value.ID, Turn: &value}})
	}
	if fresh {
		if err := emit("created"); err != nil {
			return err
		}
	}
	if (fresh || old.Status != row.Status) && row.Status != TurnWaiting {
		return emit(row.Status)
	}
	return nil
}
