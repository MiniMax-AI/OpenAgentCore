package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ItemPage struct {
	Items   []v1.Item
	HasMore bool
}

func (s *Store) ListItems(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (ItemPage, error) {
	if limit < 1 || limit > 100 {
		return ItemPage{}, ErrInvalidInput
	}
	page := ItemPage{Items: make([]v1.Item, 0, limit)}
	err := s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		p := sqlc.ListSessionItemsParams{SessionID: session, PageLimit: int32(limit + 1), Ascending: ascending, AfterID: pgtype.UUID{Valid: true}}
		if cursor != "" {
			// Any cursor that is not an Item of this Session, including a
			// malformed one, is an invalid cursor rather than a missing resource.
			row, err := q.GetSessionItem(ctx, sqlc.GetSessionItemParams{SessionID: session, ID: parsePathID(cursor)})
			if errors.Is(err, pgx.ErrNoRows) {
				return errItemCursor
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
