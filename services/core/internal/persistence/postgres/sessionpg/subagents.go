package sessionpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// LoadPublicSubagent loads a Subagent of the Session as the public API shows
// it. A missing or malformed ID is sessions.ErrNotFound.
func LoadPublicSubagent(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, id string) (v1.Subagent, error) {
	row, err := q.GetPublicSubagent(ctx, sqlc.GetPublicSubagentParams{SessionID: session, ID: pgunit.PathID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return v1.Subagent{}, sessions.ErrNotFound
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

// LoadChildTurn loads a Turn of the Session's Subagent child. A missing or
// malformed ID, a root Turn and another child's Turn are sessions.ErrNotFound.
func LoadChildTurn(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, child, id string) (sqlc.SubagentTurn, error) {
	row, err := q.GetChildTurn(ctx, sqlc.GetChildTurnParams{SessionID: session, ID: pgunit.PathID(id)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuid.UUID(row.SubagentID.Bytes).String() != child) {
		return row, sessions.ErrNotFound
	}
	return row, err
}
