package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func projectItemSource(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, kind string, sequence int64, raw json.RawMessage, created pgtype.Timestamptz) error {
	updates, err := items.Project(uuid.UUID(turn.Bytes).String(), kind, sequence, raw)
	if err != nil {
		return fmt.Errorf("project execution item: %w", err)
	}
	for _, update := range updates {
		stored, err := sessionpg.LoadItem(ctx, q, session, turn, update)
		if err != nil {
			return err
		}
		change, ok, err := items.Observe(kind, update, stored)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		index, err := sessionpg.PutItem(ctx, q, session, turn, created, change)
		if err != nil {
			return err
		}
		if err := sessionpg.AppendChanges(ctx, q, session, sessions.ItemChanges(change, index)...); err != nil {
			return err
		}
	}
	return nil
}

func indexInput(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, sequence int64) error {
	row, err := q.ItemInputSource(ctx, sqlc.ItemInputSourceParams{SessionID: session, Sequence: sequence})
	if err != nil {
		return err
	}
	if row.Kind != "message" {
		return nil
	}
	return projectSource(ctx, q, session, row.TurnID, row.Kind, row.Sequence, row.Payload, row.CreatedAt)
}

func indexEvents(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, first int32) error {
	rows, err := q.ItemEventSources(ctx, sqlc.ItemEventSourcesParams{SessionID: session, TurnID: turn, Ordinal: first})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = projectSource(ctx, q, session, turn, row.Kind, int64(row.Ordinal), row.Payload, row.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}
