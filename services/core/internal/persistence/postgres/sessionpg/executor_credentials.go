package sessionpg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (s *Store) AuthenticateEnvironmentExecutor(ctx context.Context, environment, digest string) (string, error) {
	id, err := pgunit.ParseID(environment)
	if err != nil {
		return "", sessions.ErrNotFound
	}
	hash, err := hex.DecodeString(digest)
	if err != nil || len(hash) != sha256.Size {
		return "", sessions.ErrNotFound
	}
	tenant, err := s.units.Queries().AuthenticateEnvironmentExecutor(ctx, sqlc.AuthenticateEnvironmentExecutorParams{EnvironmentID: id, TokenSha256: hex.EncodeToString(hash)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authenticate environment executor: %w", err)
	}
	return uuid.UUID(tenant.Bytes).String(), nil
}
