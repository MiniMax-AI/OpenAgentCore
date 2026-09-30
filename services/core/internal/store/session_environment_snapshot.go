package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
)

func sessionEnvironmentSnapshot(ctx context.Context, q *sqlc.Queries, session sqlc.Session) (*sessions.Environment, error) {
	row, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: session.TenantID, ID: session.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	value, err := environmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
	if err != nil {
		return nil, err
	}
	return &value, nil
}
