package sessionpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (s *Store) ListItems(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (sessions.ItemPage, error) {
	return s.listItems(ctx, tenantID, sessionID, "", cursor, limit, ascending)
}

// ListTurnItems lists the Items of one root Turn with the Session Item cursor rules.
func (s *Store) ListTurnItems(ctx context.Context, tenantID, sessionID, turnID, cursor string, limit int, ascending bool) (sessions.ItemPage, error) {
	if turnID == "" {
		return sessions.ItemPage{}, sessions.ErrInvalidInput
	}
	return s.listItems(ctx, tenantID, sessionID, turnID, cursor, limit, ascending)
}

// listItems lists the Items of the Session, or of one of its Turns when turnID is not empty.
func (s *Store) listItems(ctx context.Context, tenantID, sessionID, turnID, cursor string, limit int, ascending bool) (sessions.ItemPage, error) {
	if limit < 1 || limit > 100 {
		return sessions.ItemPage{}, sessions.ErrInvalidInput
	}
	page := sessions.ItemPage{Items: make([]v1.Item, 0, limit)}
	err := s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		p := sqlc.ListSessionItemsParams{SessionID: session, PageLimit: int32(limit + 1), Ascending: ascending, AfterID: pgtype.UUID{Valid: true}}
		if turnID != "" {
			tenant, err := parseID(tenantID)
			if err != nil {
				return err
			}
			turn, err := q.GetTurn(ctx, sqlc.GetTurnParams{TenantID: tenant, SessionID: session, ID: pgunit.PathID(turnID)})
			if errors.Is(err, pgx.ErrNoRows) {
				return sessions.ErrNotFound
			}
			if err != nil {
				return err
			}
			p.TurnID = turn.ID
		}
		if cursor != "" {
			// Any cursor that is not an Item of this Session, including a
			// malformed one, is an invalid cursor rather than a missing resource.
			row, err := q.GetSessionItem(ctx, sqlc.GetSessionItemParams{SessionID: session, ID: pgunit.PathID(cursor)})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && p.TurnID.Valid && p.TurnID != row.TurnID) {
				return sessions.ErrItemCursor
			}
			if err != nil {
				return err
			}
			p.AfterCreated = row.CreatedAt
			p.AfterID = row.ID
			p.AfterPosition = row.Position
		}
		rows, err := q.ListSessionItems(ctx, p)
		if err != nil {
			return err
		}
		page.HasMore = len(rows) > limit
		if page.HasMore {
			rows = rows[:limit]
		}
		for _, row := range rows {
			var item v1.Item
			if err = json.Unmarshal(row.Payload, &item); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return nil
	})
	return page, err
}
