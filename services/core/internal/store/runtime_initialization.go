package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) requireInitializedEnvironment(ctx context.Context, tenant, session pgtype.UUID) error {
	ready, err := s.queries.GetSessionInitializationReady(ctx, sqlc.GetSessionInitializationReadyParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !ready {
		return sessions.ErrNotFound
	}
	return nil
}
