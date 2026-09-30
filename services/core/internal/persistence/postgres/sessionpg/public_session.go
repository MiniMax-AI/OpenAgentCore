package sessionpg

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// withPublicSession runs apply in the Session transaction of the tenant's
// visible Session. A malformed tenant is sessions.ErrInvalidInput; a malformed
// Session ID resolves as a missing Session.
func (s *Store) withPublicSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	session := pgunit.PathID(sessionID)
	return WithSession(ctx, s.units, tenant, session, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		return apply(ctx, q, session)
	})
}
