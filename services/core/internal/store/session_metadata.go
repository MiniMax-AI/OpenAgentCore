package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) UpdateSessionMetadata(ctx context.Context, tenantID, sessionID string, values map[string]string) (sessions.Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Session{}, err
	}
	id := pgunit.PathID(sessionID)
	encoded, err := metadata.Encode(values)
	if err != nil {
		return sessions.Session{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	var row sqlc.Session
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		row, err = q.UpdateSessionMetadata(ctx, sqlc.UpdateSessionMetadataParams{TenantID: tenant, ID: id, Metadata: encoded})
		if err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "update", "session", uuid.UUID(row.ID.Bytes).String(), "")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Session{}, fmt.Errorf("update session metadata: %w", err)
	}
	session, decodeErr := sessionFromRow(row)
	return s.sessionActivity(ctx, session, decodeErr)
}
