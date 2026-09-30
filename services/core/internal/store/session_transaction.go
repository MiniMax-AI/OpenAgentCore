package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// All Turn admission and lifecycle writes run in sessionpg's Session
// transaction on the writer.
func (s *Store) withSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withSessionState(ctx, tenantID, sessionID, false, apply)
}

func (s *Store) withPublicSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withSessionState(ctx, tenantID, sessionID, true, apply)
}

func (s *Store) withSessionState(ctx context.Context, tenantID, sessionID string, public bool, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withLockedSession(ctx, tenantID, sessionID, public, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, locked sessions.LockedSession) error {
		if public {
			if err := locked.Public(); err != nil {
				return err
			}
		}
		return apply(ctx, q, session)
	})
}

// withLockedSession resolves the identifiers and runs apply in the Session
// transaction, including for a publicly deleted Session.
func (s *Store) withLockedSession(ctx context.Context, tenantID, sessionID string, public bool, apply func(context.Context, *sqlc.Queries, pgtype.UUID, sessions.LockedSession) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	// Public paths resolve malformed IDs as missing; internal callers keep parseID.
	id := pgunit.PathID(sessionID)
	if !public {
		if id, err = parseID(sessionID); err != nil {
			return err
		}
	}
	return sessionpg.WithSession(ctx, s.writer, tenant, id, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		return apply(ctx, q, id, locked)
	})
}
