package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeleteCredential removes the stored secret without reading or decrypting it.
func (s *Store) DeleteCredential(ctx context.Context, tenantID, vaultID, credentialID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", ErrNotFound
	}
	vault, err := parseID(vaultID)
	if err != nil {
		return "", ErrNotFound
	}
	id, err := parseID(credentialID)
	if err != nil {
		return "", ErrNotFound
	}
	var deletedID string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		deleted, err := q.DeleteCredential(ctx, sqlc.DeleteCredentialParams{TenantID: tenant, VaultID: vault, ID: id})
		if err != nil {
			return err
		}
		deletedID = uuid.UUID(deleted.Bytes).String()
		return recordWriteAudit(ctx, q, tenantID, "delete", "credential", deletedID, uuid.UUID(vault.Bytes).String())
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errors.New("credential deletion failed")
	}
	return deletedID, nil
}
