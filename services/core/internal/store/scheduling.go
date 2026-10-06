package store

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func (s *Store) ListEnvironmentInputWork(ctx context.Context, after string, connectedDevices []string) ([]sessions.EnvironmentInputWork, error) {
	id, devices, err := sessionpg.ExecutionWorkCursor(after, connectedDevices)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListEnvironmentInputWork(ctx, sqlc.ListEnvironmentInputWorkParams{AfterID: id, ConnectedDevices: devices})
	if err != nil {
		return nil, err
	}
	work := make([]sessions.EnvironmentInputWork, 0, len(rows))
	for _, row := range rows {
		work = append(work, sessions.EnvironmentInputWork{TenantID: uuid.UUID(row.TenantID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), ReservationID: uuid.UUID(row.ID.Bytes).String()})
	}
	return work, nil
}
