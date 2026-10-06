package sessionpg

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (e *Execution) WithInputs(ctx context.Context, tenant, session string, apply func(context.Context, sessions.InputTx) error) error {
	return withInputs(ctx, e.lease, tenant, session, apply)
}

// WithDueInputReservations locks each due reservation's Session with the scan
// itself, skipping Sessions another transaction holds, and prunes each
// Session's change journal after apply.
func (e *Execution) WithDueInputReservations(ctx context.Context, apply func(context.Context, sessions.InputTx, string) error) error {
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		rows, err := q.ListDueEnvironmentInputs(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := apply(ctx, BindSession(q, row.TenantID, row.SessionID), uuid.UUID(row.ID.Bytes).String()); err != nil {
				return err
			}
			if err := PruneChanges(ctx, q, row.SessionID); err != nil {
				return err
			}
		}
		return nil
	})
}
