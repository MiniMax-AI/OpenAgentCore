package sessionpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (e *Execution) WithFunctionTurn(ctx context.Context, tenant, session, turn string, apply func(context.Context, sessions.FunctionTx, sessions.Turn) error) error {
	p, err := TurnLookup(tenant, session, turn)
	if err != nil {
		return err
	}
	return WithSession(ctx, e.lease, p.TenantID, p.SessionID, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		row, err := q.GetTurn(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		return apply(ctx, BindSession(q, p.TenantID, p.SessionID), TurnFromRow(row))
	})
}
