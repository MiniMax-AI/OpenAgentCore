package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeleteVault relies on the owning foreign key to remove every stored Credential.
func (s *Store) DeleteVault(ctx context.Context, tenantID, vaultID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", ErrNotFound
	}
	id, err := parseID(vaultID)
	if err != nil {
		return "", ErrNotFound
	}
	var deletedID string
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		deleted, err := q.DeleteVault(ctx, sqlc.DeleteVaultParams{TenantID: tenant, ID: id})
		if err != nil {
			return err
		}
		deletedID = uuid.UUID(deleted.Bytes).String()
		return recordWriteAudit(ctx, q, tenantID, "delete", "vault", deletedID, "")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errors.New("vault deletion failed")
	}
	return deletedID, nil
}
