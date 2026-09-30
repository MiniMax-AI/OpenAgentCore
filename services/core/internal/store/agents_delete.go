package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeleteAgent removes a saved resource independently of execution snapshots.
func (s *Store) DeleteAgent(ctx context.Context, tenantID, agentID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", err
	}
	id, err := parseID(agentID)
	if err != nil {
		return "", err
	}
	var deletedID string
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		deleted, err := q.DeleteAgent(ctx, sqlc.DeleteAgentParams{TenantID: tenant, ID: id})
		if err != nil {
			return err
		}
		deletedID = uuid.UUID(deleted.Bytes).String()
		return recordWriteAudit(ctx, q, tenantID, "delete", "agent", deletedID, "")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete agent: %w", err)
	}
	return deletedID, nil
}
