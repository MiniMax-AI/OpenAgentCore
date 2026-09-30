package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReconcileEnvironmentConnections runs before the new owner's connection producers start.
// It discards previous process generations and records loss of their connected transport.
func (s *Store) ReconcileEnvironmentConnections(ctx context.Context) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	after := pgtype.UUID{Valid: true}
	for {
		var rows []sqlc.ListEnvironmentConnectionsRow
		err := s.writer.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			rows, err = s.queries.WithTx(tx).ListEnvironmentConnections(ctx, after)
			return err
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			err := s.withEnvironmentConnection(ctx, uuid.UUID(row.TenantID.Bytes).String(), uuid.UUID(row.ID.Bytes).String(), func(ctx context.Context, q *sqlc.Queries, current sqlc.GetSessionEnvironmentRow) error {
				if err := q.DeleteEnvironmentConnection(ctx, current.Environment.ID); err != nil {
					return err
				}
				if current.Environment.Status == "connected" {
					return recordEnvironmentConnection(ctx, q, current, "disconnected")
				}
				return nil
			})
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			after = row.ID
		}
	}
}
