package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) UpdateSessionMetadata(ctx context.Context, tenantID, sessionID string, metadata map[string]string) (Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Session{}, err
	}
	id := parsePathID(sessionID)
	encoded, err := encodeMetadata(metadata)
	if err != nil {
		return Session{}, err
	}
	var row sqlc.Session
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		row, err = q.UpdateSessionMetadata(ctx, sqlc.UpdateSessionMetadataParams{TenantID: tenant, ID: id, Metadata: encoded})
		if err != nil {
			return err
		}
		return recordWriteAudit(ctx, q, tenantID, "update", "session", uuid.UUID(row.ID.Bytes).String(), "")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("update session metadata: %w", err)
	}
	session, decodeErr := sessionFromRow(row)
	return s.sessionActivity(ctx, session, decodeErr)
}

func encodeMetadata(metadata map[string]string) ([]byte, error) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("%w: metadata: %v", ErrInvalidInput, err)
	}
	if len(encoded) > 64*1024 {
		return nil, fmt.Errorf("%w: metadata exceeds 64 KiB", ErrInvalidInput)
	}
	return encoded, nil
}
