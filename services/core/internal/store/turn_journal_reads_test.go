package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ListTurnEvents reads a Turn's journal after the given ordinal. Only tests
// read the journal back.
func (s *Store) ListTurnEvents(ctx context.Context, tenantID, sessionID, turnID string, after int32, limit int) ([]sessions.TurnEvent, error) {
	p, err := sessionpg.TurnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > 100 {
		return nil, sessions.ErrInvalidInput
	}
	if _, err = s.GetTurn(ctx, tenantID, sessionID, turnID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListTurnEvents(ctx, sqlc.ListTurnEventsParams{TenantID: p.TenantID, SessionID: p.SessionID, TurnID: p.ID, Ordinal: after, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	events := make([]sessions.TurnEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, sessions.TurnEvent{Ordinal: row.Ordinal, Kind: row.Kind, Payload: row.Payload, CreatedAt: row.CreatedAt.Time})
	}
	return events, nil
}

// SubagentIdentity is a Subagent's internal execution binding, as tests read it
// back.
type SubagentIdentity struct {
	ID, SessionID, NativeID, ParentNativeID string
	NativeCreatedAt                         int64
	FirstTurnID                             string
	FirstEventOrdinal                       int32
	FirstObservedAt                         time.Time
}

// GetSubagentIdentity reads a binding in its authorized, visible Session. Only
// tests read bindings back.
func (s *Store) GetSubagentIdentity(ctx context.Context, tenantID, sessionID, nativeID string) (SubagentIdentity, error) {
	p, err := sessionpg.DeviceLookup(tenantID, sessionID)
	if err != nil {
		return SubagentIdentity{}, err
	}
	row, err := s.queries.GetSubagentIdentity(ctx, sqlc.GetSubagentIdentityParams{TenantID: p.TenantID, SessionID: p.ID, NativeID: nativeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return SubagentIdentity{}, sessions.ErrNotFound
	}
	if err != nil {
		return SubagentIdentity{}, err
	}
	return SubagentIdentity{ID: uuid.UUID(row.ID.Bytes).String(), SessionID: sessionID,
		NativeID: row.NativeID, ParentNativeID: row.ParentNativeID, NativeCreatedAt: row.NativeCreatedAt,
		FirstTurnID: uuid.UUID(row.FirstTurnID.Bytes).String(), FirstEventOrdinal: row.FirstEventOrdinal,
		FirstObservedAt: row.FirstObservedAt.Time}, nil
}
