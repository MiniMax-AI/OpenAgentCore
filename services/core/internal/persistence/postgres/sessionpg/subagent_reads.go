package sessionpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var _ sessions.SubagentReader = (*Store)(nil)

func (s *Store) GetSubagent(ctx context.Context, tenantID, sessionID, subagentID string) (v1.Subagent, error) {
	var result v1.Subagent
	err := s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		var err error
		result, err = LoadPublicSubagent(ctx, q, session, subagentID)
		return err
	})
	return result, err
}

func (s *Store) ListSubagents(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (v1.SubagentList, error) {
	result := v1.SubagentList{Data: []v1.Subagent{}}
	if limit < 1 || limit > 100 {
		return result, sessions.ErrInvalidInput
	}
	err := s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		p := sqlc.ListPublicSubagentsParams{SessionID: session, Ascending: ascending, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}}
		if cursor != "" {
			// Any cursor that is not a Subagent of this Session, including a
			// malformed one, is an invalid cursor rather than a missing resource.
			after, err := LoadPublicSubagent(ctx, q, session, cursor)
			if err != nil {
				return unresolvedCursor(err, sessions.ErrResourceCursor)
			}
			p.AfterOpened = pgtype.Int8{Int64: after.OpenedAt, Valid: true}
			p.AfterID, _ = parseID(cursor)
		}
		rows, err := q.ListPublicSubagents(ctx, p)
		if err != nil {
			return err
		}
		result.HasMore = len(rows) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for _, id := range rows {
			value, err := LoadPublicSubagent(ctx, q, session, uuid.UUID(id.Bytes).String())
			if err != nil {
				return err
			}
			result.Data = append(result.Data, value)
		}
		return nil
	})
	return result, err
}

func (s *Store) GetSubagentTurn(ctx context.Context, tenantID, sessionID, subagentID, turnID string) (v1.Turn, error) {
	var result v1.Turn
	err := s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := LoadPublicSubagent(ctx, q, session, subagentID); err != nil {
			return err
		}
		row, err := LoadChildTurn(ctx, q, session, subagentID, turnID)
		if err != nil {
			return err
		}
		agent, err := sessionAgentID(ctx, q, session)
		if err == nil {
			result = publicChildTurn(row, agent)
		}
		return err
	})
	return result, err
}

func (s *Store) ListSubagentTurns(ctx context.Context, tenantID, sessionID, subagentID, cursor string, limit int, ascending bool) (v1.TurnList, error) {
	result := v1.TurnList{Data: []v1.Turn{}}
	if limit < 1 || limit > 100 {
		return result, sessions.ErrInvalidInput
	}
	err := s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := LoadPublicSubagent(ctx, q, session, subagentID); err != nil {
			return err
		}
		child, _ := parseID(subagentID)
		p := sqlc.ListChildTurnsParams{SessionID: session, SubagentID: child, Ascending: ascending, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}}
		if cursor != "" {
			// Root Turns and other children's Turns are outside this list.
			row, err := LoadChildTurn(ctx, q, session, subagentID, cursor)
			if err != nil {
				return unresolvedCursor(err, sessions.ErrResourceCursor)
			}
			p.AfterCreated = row.CreatedAt
			p.AfterID = row.ID
		}
		rows, err := q.ListChildTurns(ctx, p)
		if err != nil {
			return err
		}
		agent, err := sessionAgentID(ctx, q, session)
		if err != nil {
			return err
		}
		result.HasMore = len(rows) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for _, row := range rows {
			result.Data = append(result.Data, publicChildTurn(row, agent))
		}
		return nil
	})
	return result, err
}

func (s *Store) ListSubagentItems(ctx context.Context, tenantID, sessionID, subagentID, cursor string, limit int, ascending bool) (v1.ItemList, error) {
	return s.listChildItems(ctx, tenantID, sessionID, subagentID, "", cursor, limit, ascending)
}

func (s *Store) ListSubagentTurnItems(ctx context.Context, tenantID, sessionID, subagentID, turnID, cursor string, limit int, ascending bool) (v1.ItemList, error) {
	if turnID == "" {
		return v1.ItemList{}, sessions.ErrInvalidInput
	}
	return s.listChildItems(ctx, tenantID, sessionID, subagentID, turnID, cursor, limit, ascending)
}

// listChildItems lists the Items of the Subagent, or of one of its Turns when
// turnID is not empty.
func (s *Store) listChildItems(ctx context.Context, tenantID, sessionID, subagentID, turnID, cursor string, limit int, ascending bool) (v1.ItemList, error) {
	result := v1.ItemList{Data: []v1.Item{}}
	if limit < 1 || limit > 100 {
		return result, sessions.ErrInvalidInput
	}
	err := s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := LoadPublicSubagent(ctx, q, session, subagentID); err != nil {
			return err
		}
		child, _ := parseID(subagentID)
		p := sqlc.ListChildItemsParams{SessionID: session, SubagentID: child, Ascending: ascending, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, AfterTurn: pgtype.UUID{Valid: true}}
		if turnID != "" {
			row, err := LoadChildTurn(ctx, q, session, subagentID, turnID)
			if err != nil {
				return err
			}
			p.TurnID = row.ID
		}
		if cursor != "" {
			// Any cursor outside this child (and Turn) scope, including a
			// malformed one, uses the Session Item cursor error.
			row, err := q.GetChildItem(ctx, sqlc.GetChildItemParams{SessionID: session, SubagentID: child, ID: pgunit.PathID(cursor)})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && p.TurnID.Valid && p.TurnID != row.TurnID) {
				return sessions.ErrItemCursor
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

// sessionAgentID returns the Session's Agent ID, which is the agent_id of every
// Turn in the Session, including child Turns. The official service projects a
// direct child's Turn this way; nested children follow the same rule unobserved.
func sessionAgentID(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (string, error) {
	agent, err := q.SubagentRootAgent(ctx, session)
	if err == nil && agent == "" {
		err = errors.New("missing stored agent identity")
	}
	return agent, err
}

// publicChildTurn identifies the child through subagent_id; agent_id is the
// Session's Agent ID.
func publicChildTurn(row sqlc.SubagentTurn, agent string) v1.Turn {
	child := uuid.UUID(row.SubagentID.Bytes).String()
	value := v1.Turn{ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), AgentID: agent, SubagentID: &child, Object: "agent.session.turn", Status: row.Status, CreatedAt: row.CreatedAt.Time.Unix()}
	if row.StartedAt.Valid {
		seconds := row.StartedAt.Time.Unix()
		value.StartedAt = &seconds
	}
	if row.CompletedAt.Valid {
		seconds := row.CompletedAt.Time.Unix()
		value.CompletedAt = &seconds
	}
	_ = json.Unmarshal(row.TokenUsage, &value.Usage)
	if row.Status == sessions.TurnFailed {
		value.Error = &v1.TurnError{Code: "internal_error", Message: "The execution could not complete."}
	}
	return value
}

// unresolvedCursor replaces a missing cursor resource with the list's cursor
// error and keeps every other failure.
func unresolvedCursor(err, cursor error) error {
	if errors.Is(err, sessions.ErrNotFound) {
		return cursor
	}
	return err
}
