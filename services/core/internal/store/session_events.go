package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
)

func (s *Store) SessionEventCursor(ctx context.Context, tenantID, sessionID string) (int64, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return 0, err
	}
	id := pgunit.PathID(sessionID)
	cursor, err := s.queries.SessionEventCursor(ctx, sqlc.SessionEventCursorParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, sessions.ErrNotFound
	}
	return cursor, err
}

func (s *Store) ListSessionEvents(ctx context.Context, tenantID, sessionID string, after int64) ([]sessions.SessionChange, error) {
	if after < 0 {
		return nil, sessions.ErrInvalidInput
	}
	latest, err := s.SessionEventCursor(ctx, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSessionEvents(ctx, sqlc.ListSessionEventsParams{TenantID: tenant, SessionID: id, Sequence: after})
	if err != nil {
		return nil, err
	}
	changes := make([]sessions.SessionChange, 0, len(rows))
	if len(rows) == 0 && latest > after {
		return nil, sessions.ErrStreamGap
	}
	for _, row := range rows {
		if row.Sequence != after+1 {
			return nil, sessions.ErrStreamGap
		}
		var change sessions.SessionChange
		decoder := json.NewDecoder(bytes.NewReader(row.Payload))
		decoder.UseNumber()
		if err := decoder.Decode(&change); err != nil {
			return nil, err
		}
		change.Sequence = row.Sequence
		changes = append(changes, change)
		after = row.Sequence
	}
	return changes, nil
}
