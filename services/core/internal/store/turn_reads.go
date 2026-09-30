package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ListTurns pages a Session's root Turns. Subagent Turns are not Session Turns;
// sessionpg.Store.ListSubagentTurns reads them.
func (s *Store) ListTurns(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (sessions.TurnPage, error) {
	if limit < 1 || limit > 100 {
		return sessions.TurnPage{}, fmt.Errorf("%w: page size must be 1..100", sessions.ErrInvalidInput)
	}
	if _, err := s.GetSession(ctx, tenantID, sessionID); err != nil {
		return sessions.TurnPage{}, err
	}
	tenant, _ := parseID(tenantID)
	session, _ := parseID(sessionID)
	params := sqlc.ListRootTurnsParams{TenantID: tenant, SessionID: session, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		// A child Turn is not a Session Turn, so its ID is a missing cursor here.
		after, err := s.GetTurn(ctx, tenantID, sessionID, pgunit.LookupCursor(cursor))
		if err != nil {
			return sessions.TurnPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListRootTurns(ctx, params)
	if err != nil {
		return sessions.TurnPage{}, fmt.Errorf("list turns: %w", err)
	}
	page := sessions.TurnPage{Turns: make([]sessions.Turn, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Turns = append(page.Turns, sessionpg.TurnFromRow(row))
	}
	return page, nil
}
