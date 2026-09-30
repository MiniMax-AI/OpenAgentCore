package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// withEnvironmentInputSession runs apply in the public Session's transaction
// and reports the input activity it changes.
func (s *Store) withEnvironmentInputSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	return s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, id pgtype.UUID) error {
		return sessions.TrackInputActivity(ctx, sessionpg.BindSession(q, tenant, id), func(ctx context.Context) error { return apply(ctx, q, id) })
	})
}
