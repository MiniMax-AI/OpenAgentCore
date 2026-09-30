package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (s *Store) GetSessionExecutionBinding(ctx context.Context, tenantID, sessionID string) (sessions.ExecutionBinding, error) {
	params, err := sessionpg.DeviceLookup(tenantID, sessionID)
	if err != nil {
		return sessions.ExecutionBinding{}, err
	}
	if err := s.requireInitializedEnvironment(ctx, params.TenantID, params.ID); err != nil {
		return sessions.ExecutionBinding{}, err
	}
	row, err := s.queries.GetSessionExecutionBinding(ctx, sqlc.GetSessionExecutionBindingParams(params))
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ExecutionBinding{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.ExecutionBinding{}, err
	}
	return sessions.ExecutionBinding{
		Device:          executionDevice(row.ID, row.Name, row.EnvironmentID),
		NativeSessionID: row.NativeSessionID,
		HasStartedTurn:  row.HasStartedTurn,
	}, nil
}

func executionDevice(id pgtype.UUID, name string, environmentID pgtype.UUID) sessions.ExecutionDevice {
	value := sessions.ExecutionDevice{ID: uuid.UUID(id.Bytes).String(), Name: name}
	if environmentID.Valid {
		value.EnvironmentID = uuid.UUID(environmentID.Bytes).String()
	}
	return value
}
