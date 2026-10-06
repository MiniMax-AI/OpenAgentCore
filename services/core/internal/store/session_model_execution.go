package store

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) saveSessionModelExecution(ctx context.Context, q *sqlc.Queries, tenant string, session pgtype.UUID, provider *v1.ModelProviderInput) error {
	if provider == nil {
		return nil
	}
	raw, err := json.Marshal(provider)
	if err != nil {
		return err
	}
	encrypted, err := s.credentialCipher.SealModelExecution(raw, tenant, uuid.UUID(session.Bytes).String())
	if err != nil {
		return credentialcrypto.ErrUnavailable
	}
	return q.SaveSessionModelExecution(ctx, sqlc.SaveSessionModelExecutionParams{SessionID: session, EncryptedConfig: encrypted})
}
