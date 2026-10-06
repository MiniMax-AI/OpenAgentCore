package store

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) sealEnvironmentSetup(tenant, resource, id, field string, input any) ([]byte, error) {
	plaintext, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return s.credentialCipher.SealEnvironmentSetup(plaintext, credentialcrypto.EnvironmentSetupBinding{TenantID: tenant, Resource: resource, OwnerID: id, Field: field})
}

func (s *Store) saveEnvironmentSetup(ctx context.Context, q *sqlc.Queries, tenant string, session pgtype.UUID, setup environmentconfig.Setup) error {
	if setup.ValidateInstalled() != nil {
		return sessions.ErrInvalidInput
	}
	if setup.Empty() {
		return nil
	}
	owner, err := parseID(tenant)
	if err != nil {
		return err
	}
	encrypted, err := s.sealEnvironmentSetup(uuid.UUID(owner.Bytes).String(), "session", uuid.UUID(session.Bytes).String(), "initialization", setup)
	if err != nil {
		return err
	}
	return q.CreateEnvironmentSetup(ctx, sqlc.CreateEnvironmentSetupParams{SessionID: session, Contents: encrypted})
}
