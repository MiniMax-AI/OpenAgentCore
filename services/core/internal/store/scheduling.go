package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func executionWorkCursor(after string, connectedDevices []string) (pgtype.UUID, []pgtype.UUID, error) {
	id := pgtype.UUID{Valid: true}
	var err error
	if after != "" {
		id, err = parseID(after)
		if err != nil {
			return id, nil, err
		}
	}
	devices := make([]pgtype.UUID, 0, len(connectedDevices))
	for _, value := range connectedDevices {
		device, err := parseID(value)
		if err != nil {
			return id, nil, err
		}
		devices = append(devices, device)
	}
	return id, devices, nil
}

func (s *Store) ListEnvironmentInputWork(ctx context.Context, after string, connectedDevices []string) ([]sessions.EnvironmentInputWork, error) {
	id, devices, err := executionWorkCursor(after, connectedDevices)
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

func (s *Store) ListExecutionWork(ctx context.Context, after string, statuses []string, connectedDevices []string) ([]sessions.ExecutionWork, error) {
	id, devices, err := executionWorkCursor(after, connectedDevices)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListExecutionWork(ctx, sqlc.ListExecutionWorkParams{AfterID: id, Statuses: statuses, ConnectedOnly: connectedDevices != nil, ConnectedDevices: devices})
	if err != nil {
		return nil, err
	}
	work := make([]sessions.ExecutionWork, 0, len(rows))
	for _, row := range rows {
		work = append(work, sessions.ExecutionWork{TenantID: uuid.UUID(row.TenantID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), TurnID: uuid.UUID(row.ID.Bytes).String(), Status: row.Status})
	}
	return work, nil
}

func (s *Store) ListExecutionDevices(ctx context.Context, tenantID string) ([]sessions.ExecutionDevice, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListExecutionDevices(ctx, tenant)
	if err != nil {
		return nil, err
	}
	devices := make([]sessions.ExecutionDevice, 0, len(rows))
	for _, row := range rows {
		devices = append(devices, sessions.ExecutionDevice{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name})
	}
	return devices, nil
}

func (s *Store) sessionActivity(ctx context.Context, session sessions.Session, err error) (sessions.Session, error) {
	if err != nil {
		return sessions.Session{}, err
	}
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		session, err = readSessionActivity(ctx, s.queries.WithTx(tx), session)
		return err
	})
	return session, err
}

// SessionStreamSnapshot reads the projection that GetSession returns and the
// committed Session event cursor from one database snapshot.
func (s *Store) SessionStreamSnapshot(ctx context.Context, tenantID, sessionID string) (sessions.Session, int64, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Session{}, 0, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return sessions.Session{}, 0, err
	}
	var session sessions.Session
	var cursor int64
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: id})
		if err != nil {
			return err
		}
		if session, err = sessionpg.SessionFromRow(row); err != nil {
			return err
		}
		cursor = row.EventSequence
		session, err = readSessionActivity(ctx, q, session)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, 0, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Session{}, 0, fmt.Errorf("read session stream snapshot: %w", err)
	}
	return session, cursor, nil
}

// readSessionActivity adds the Environment, reservation activity and latest Turn
// projection within the caller's snapshot.
func readSessionActivity(ctx context.Context, q *sqlc.Queries, session sessions.Session) (sessions.Session, error) {
	id, _ := parseID(session.ID)
	tenant, _ := parseID(session.TenantID)
	environment, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: id})
	if err == nil {
		value, err := sessionpg.EnvironmentFromRow(environment.Environment, environment.TenantID, environment.Configuration, nil)
		if err != nil {
			return session, err
		}
		session.Environment = &value
		session.EnvironmentFailure = environmentFailure(environment.Environment)
		state, err := sessionpg.LoadEnvironmentInput(ctx, q, id)
		if err != nil {
			return session, err
		}
		session.EnvironmentInputActivity, session.PendingInput = sessions.InputActivity(state)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return session, err
	}
	row, err := q.GetLatestSessionTurn(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, nil
	}
	if err != nil {
		return session, err
	}
	turn := sessionpg.TurnFromRow(row)
	session.LastTurn = &turn
	session.RequiredActions, err = functionActions(ctx, q, row)
	if err != nil {
		return session, err
	}
	session.Usage, err = q.SessionTokenUsage(ctx, id)
	return session, err
}

func environmentFailure(row sqlc.Environment) *sessions.EnvironmentFailure {
	if row.Status != "failed" || !row.FailureReason.Valid || !row.FailedAt.Valid {
		return nil
	}
	failure := &sessions.EnvironmentFailure{Reason: row.FailureReason.String, FailedAt: row.FailedAt.Time}
	var detail sessions.ProvisioningFailureDetail
	if json.Unmarshal(row.FailureDetail, &detail) == nil {
		failure.Detail = sessions.SanitizedProvisioningDetail(detail)
	}
	return failure
}
