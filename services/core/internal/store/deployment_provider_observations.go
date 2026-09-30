package store

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
)

// ObserveDeploymentModelProvider attempts one metadata UPDATE through the pool,
// never the leased execution connection. SQL verifies the committed root outcome
// and exact frozen revision without loading credentials. The metadata transaction
// installs server-side timeouts: client cancellation alone can leave PostgreSQL
// executing briefly after pgx has returned a deadline error.
func (s *Store) ObserveDeploymentModelProvider(ctx context.Context, tenantID, sessionID, turnID string) (int64, error) {
	lookup, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var count int64
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		deadline, _ := ctx.Deadline()
		// Leave a small part of the overall budget for returning the server error
		// and releasing this metadata-only transaction before the client deadline.
		timeout := time.Until(deadline).Milliseconds() - 25
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
		setting := strconv.FormatInt(timeout, 10) + "ms"
		if _, err := tx.Exec(ctx, "SELECT set_config('statement_timeout', $1, true), set_config('lock_timeout', $1, true)", setting); err != nil {
			return err
		}
		var err error
		count, err = sqlc.New(tx).ObserveDeploymentModelProvider(ctx, sqlc.ObserveDeploymentModelProviderParams{
			TenantID: lookup.TenantID, SessionID: lookup.SessionID, TurnID: lookup.ID,
		})
		return err
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
