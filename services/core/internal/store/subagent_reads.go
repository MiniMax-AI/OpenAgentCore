package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func publicSubagent(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, id string) (v1.Subagent, error) {
	row, err := q.GetPublicSubagent(ctx, sqlc.GetPublicSubagentParams{SessionID: session, ID: pgunit.PathID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return v1.Subagent{}, ErrNotFound
	}
	if err != nil {
		return v1.Subagent{}, err
	}
	result := v1.Subagent{ID: id, SessionID: uuid.UUID(session.Bytes).String(), ParentAgentID: row.ParentAgentID, Object: "agent.session.subagent", Status: row.Status, OpenedAt: row.NativeCreatedAt}
	if row.Name.Valid {
		result.Name = &row.Name.String
	}
	if row.Instructions.Valid {
		result.Instructions = []v1.AgentContent{{Type: "output_text", Text: &row.Instructions.String}}
	}
	if row.ClosedAtMs.Valid {
		seconds := row.ClosedAtMs.Int64 / 1000
		result.ClosedAt = &seconds
	}
	return result, nil
}

func (s *Store) GetSubagent(ctx context.Context, tenant, session, id string) (v1.Subagent, error) {
	var result v1.Subagent
	err := s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, sid pgtype.UUID) error {
		var err error
		result, err = publicSubagent(ctx, q, sid, id)
		return err
	})
	return result, err
}

func (s *Store) ListSubagents(ctx context.Context, tenant, session, after string, limit int, asc bool) (v1.SubagentList, error) {
	result := v1.SubagentList{Data: []v1.Subagent{}}
	if limit < 1 || limit > 100 {
		return result, ErrInvalidInput
	}
	err := s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, sid pgtype.UUID) error {
		p := sqlc.ListPublicSubagentsParams{SessionID: sid, Ascending: asc, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}}
		if after != "" {
			// Any cursor that is not a Subagent of this Session, including a
			// malformed one, is an invalid cursor rather than a missing resource.
			cursor, err := publicSubagent(ctx, q, sid, after)
			if err != nil {
				return unresolvedCursor(err, errResourceCursor)
			}
			p.AfterOpened = pgtype.Int8{Int64: cursor.OpenedAt, Valid: true}
			p.AfterID, _ = parseID(after)
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
			value, err := publicSubagent(ctx, q, sid, uuid.UUID(id.Bytes).String())
			if err != nil {
				return err
			}
			result.Data = append(result.Data, value)
		}
		return nil
	})
	return result, err
}

func childTurn(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, child, id string) (sqlc.SubagentTurn, error) {
	row, err := q.GetChildTurn(ctx, sqlc.GetChildTurnParams{SessionID: session, ID: pgunit.PathID(id)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuid.UUID(row.SubagentID.Bytes).String() != child) {
		return row, ErrNotFound
	}
	return row, err
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

func (s *Store) GetSubagentTurn(ctx context.Context, tenant, session, child, id string) (v1.Turn, error) {
	var result v1.Turn
	err := s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, sid pgtype.UUID) error {
		if _, err := publicSubagent(ctx, q, sid, child); err != nil {
			return err
		}
		row, err := childTurn(ctx, q, sid, child, id)
		if err != nil {
			return err
		}
		agent, err := sessionAgentID(ctx, q, sid)
		if err == nil {
			result = publicChildTurn(row, agent)
		}
		return err
	})
	return result, err
}

func (s *Store) ListSubagentTurns(ctx context.Context, tenant, session, child, after string, limit int, asc bool) (v1.TurnList, error) {
	result := v1.TurnList{Data: []v1.Turn{}}
	if limit < 1 || limit > 100 {
		return result, ErrInvalidInput
	}
	err := s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, sid pgtype.UUID) error {
		if _, err := publicSubagent(ctx, q, sid, child); err != nil {
			return err
		}
		childID, _ := parseID(child)
		p := sqlc.ListChildTurnsParams{SessionID: sid, SubagentID: childID, Ascending: asc, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}}
		if after != "" {
			// Root Turns and other children's Turns are outside this list.
			row, err := childTurn(ctx, q, sid, child, after)
			if err != nil {
				return unresolvedCursor(err, errResourceCursor)
			}
			p.AfterCreated = row.CreatedAt
			p.AfterID = row.ID
		}
		rows, err := q.ListChildTurns(ctx, p)
		if err != nil {
			return err
		}
		agent, err := sessionAgentID(ctx, q, sid)
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
