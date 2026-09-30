package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type TurnPage struct {
	Turns      []sessions.Turn
	NextCursor string
}

// ListTurns pages a Session's root Turns. Subagent Turns are not Session Turns;
// ListSubagentTurns reads them.
func (s *Store) ListTurns(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (TurnPage, error) {
	if limit < 1 || limit > 100 {
		return TurnPage{}, fmt.Errorf("%w: page size must be 1..100", ErrInvalidInput)
	}
	if _, err := s.GetSession(ctx, tenantID, sessionID); err != nil {
		return TurnPage{}, err
	}
	tenant, _ := parseID(tenantID)
	session, _ := parseID(sessionID)
	params := sqlc.ListRootTurnsParams{TenantID: tenant, SessionID: session, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		// A child Turn is not a Session Turn, so its ID is a missing cursor here.
		after, err := s.GetTurn(ctx, tenantID, sessionID, pgunit.LookupCursor(cursor))
		if err != nil {
			return TurnPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListRootTurns(ctx, params)
	if err != nil {
		return TurnPage{}, fmt.Errorf("list turns: %w", err)
	}
	page := TurnPage{Turns: make([]sessions.Turn, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Turns = append(page.Turns, turnFromRow(row))
	}
	return page, nil
}
