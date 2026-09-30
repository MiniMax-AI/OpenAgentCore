package store

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// CreateEnvironmentDevice provisions one dedicated Runtime without widening an existing credential.
func (s *Store) CreateEnvironmentDevice(ctx context.Context, tenantID, environmentID, name, credentialHash string) (sessions.ExecutionDevice, error) {
	environment, err := s.GetEnvironment(ctx, tenantID, environmentID)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	if kind, err := sessions.EnvironmentType(environment.Configuration); err != nil || kind != "openai_hosted" {
		return sessions.ExecutionDevice{}, sessions.ErrInvalidInput
	}
	lookup, err := sessionpg.DeviceLookup(tenantID, environment.ID)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	params, err := newDeviceParams(lookup.TenantID, name, credentialHash)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	device := sessions.ExecutionDevice{ID: uuid.UUID(params.ID.Bytes).String(), Name: params.Name, EnvironmentID: environment.ID}
	err = s.withPublicSession(ctx, tenantID, environment.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		return sessions.CreateEnvironmentDevice(ctx, sessionpg.BindSession(q, lookup.TenantID, session), device, params.CredentialHash.String)
	})
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	return device, nil
}
