package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ListSubagentItems(ctx context.Context, tenant, session, child, after string, limit int, asc bool) (v1.ItemList, error) {
	return s.listChildItems(ctx, tenant, session, child, "", after, limit, asc)
}
func (s *Store) ListSubagentTurnItems(ctx context.Context, tenant, session, child, turn, after string, limit int, asc bool) (v1.ItemList, error) {
	if turn == "" {
		return v1.ItemList{}, ErrInvalidInput
	}
	return s.listChildItems(ctx, tenant, session, child, turn, after, limit, asc)
}
func (s *Store) listChildItems(ctx context.Context, tenant, session, child, turn, after string, limit int, asc bool) (v1.ItemList, error) {
	result := v1.ItemList{Data: []v1.Item{}}
	if limit < 1 || limit > 100 {
		return result, ErrInvalidInput
	}
	err := s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, sid pgtype.UUID) error {
		if _, err := publicSubagent(ctx, q, sid, child); err != nil {
			return err
		}
		childID, _ := parseID(child)
		p := sqlc.ListChildItemsParams{SessionID: sid, SubagentID: childID, Ascending: asc, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, AfterTurn: pgtype.UUID{Valid: true}}
		if turn != "" {
			row, err := childTurn(ctx, q, sid, child, turn)
			if err != nil {
				return err
			}
			p.TurnID = row.ID
		}
		if after != "" {
			// Any cursor outside this child (and Turn) scope, including a
			// malformed one, uses the Session Item cursor error.
			row, err := q.GetChildItem(ctx, sqlc.GetChildItemParams{SessionID: sid, SubagentID: childID, ID: pgunit.PathID(after)})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && p.TurnID.Valid && p.TurnID != row.TurnID) {
				return errItemCursor
			}
			if err != nil {
				return err
			}
			p.AfterID = row.ID
			p.AfterCreated = row.TurnCreatedAt
			p.AfterTurn = row.TurnID
			p.AfterPosition = row.Position
		}
		rows, err := q.ListChildItems(ctx, p)
		if err != nil {
			return err
		}
		result.HasMore = len(rows) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for _, raw := range rows {
			var item v1.Item
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			result.Data = append(result.Data, item)
		}
		return nil
	})
	return result, err
}
