package store

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AuditSessionOperation records an authorized public no-op or creation replay.
// It cannot create ownership or admit execution work.
func (s *Store) AuditSessionOperation(ctx context.Context, tenantID, sessionID, action string) error {
	if action != "create" && action != "send_events" {
		return ErrInvalidInput
	}
	return s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		return recordWriteAudit(ctx, q, tenantID, action, "session", uuid.UUID(session.Bytes).String(), "")
	})
}
