package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) requireInitializedEnvironment(ctx context.Context, tenant, session pgtype.UUID) error {
	ready, err := s.queries.GetSessionInitializationReady(ctx, sqlc.GetSessionInitializationReadyParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !ready {
		return ErrNotFound
	}
	return nil
}
