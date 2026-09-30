package store

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
)

// ExpireEnvironmentInputs settles one bounded batch without creating Turn history.
// Only the current execution writer may run this cross-Session maintenance.
func (s *Store) ExpireEnvironmentInputs(ctx context.Context) (int64, error) {
	if err := s.checkExecutionAuthority(); err != nil {
		return 0, err
	}
	var expired int64
	err := s.writer.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		rows, err := q.ListDueEnvironmentInputs(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			err := sessions.TrackInputActivity(ctx, sessionpg.BindSession(q, row.TenantID, row.SessionID), func(ctx context.Context) error {
				return q.ExpireEnvironmentInputReservation(ctx, sqlc.ExpireEnvironmentInputReservationParams{SessionID: row.SessionID, ID: row.ID})
			})
			if err != nil {
				return err
			}
			if err := sessionpg.PruneChanges(ctx, q, row.SessionID); err != nil {
				return err
			}
			expired++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}
