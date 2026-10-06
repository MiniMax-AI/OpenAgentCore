package sessionpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var _ sessions.TurnReader = (*Store)(nil)

func (s *Store) GetTurn(ctx context.Context, tenant, session, turn string) (sessions.Turn, error) {
	lookup, err := publicTurnLookup(tenant, session, turn)
	if err != nil {
		return sessions.Turn{}, err
	}
	row, err := s.units.Queries().GetTurn(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Turn{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Turn{}, fmt.Errorf("get turn: %w", err)
	}
	return TurnFromRow(row), nil
}

func (s *Store) ListTurns(ctx context.Context, tenant, session, cursor string, limit int, ascending bool) (sessions.TurnPage, error) {
	if limit < 1 || limit > 100 {
		return sessions.TurnPage{}, fmt.Errorf("%w: page size must be 1..100", sessions.ErrInvalidInput)
	}
	tenantID, err := parseID(tenant)
	if err != nil {
		return sessions.TurnPage{}, err
	}
	sessionID := pgunit.PathID(session)
	q := s.units.Queries()
	if _, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenantID, ID: sessionID}); errors.Is(err, pgx.ErrNoRows) {
		return sessions.TurnPage{}, sessions.ErrNotFound
	} else if err != nil {
		return sessions.TurnPage{}, fmt.Errorf("get session: %w", err)
	}
	params := sqlc.ListRootTurnsParams{TenantID: tenantID, SessionID: sessionID, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		// A child Turn is not a Session Turn, so its ID is a missing cursor here.
		after, err := s.GetTurn(ctx, tenant, session, pgunit.LookupCursor(cursor))
		if err != nil {
			return sessions.TurnPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := q.ListRootTurns(ctx, params)
	if err != nil {
		return sessions.TurnPage{}, fmt.Errorf("list turns: %w", err)
	}
	page := sessions.TurnPage{Turns: make([]sessions.Turn, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Turns = append(page.Turns, TurnFromRow(row))
	}
	return page, nil
}

func (s *Store) ListExecutionWork(ctx context.Context, after string, statuses, connectedDevices []string) ([]sessions.ExecutionWork, error) {
	id, devices, err := ExecutionWorkCursor(after, connectedDevices)
	if err != nil {
		return nil, err
	}
	rows, err := s.units.Queries().ListExecutionWork(ctx, sqlc.ListExecutionWorkParams{AfterID: id, Statuses: statuses, ConnectedOnly: connectedDevices != nil, ConnectedDevices: devices})
	if err != nil {
		return nil, err
	}
	work := make([]sessions.ExecutionWork, 0, len(rows))
	for _, row := range rows {
		work = append(work, sessions.ExecutionWork{TenantID: uuid.UUID(row.TenantID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), TurnID: uuid.UUID(row.ID.Bytes).String(), Status: row.Status})
	}
	return work, nil
}

// ExecutionWorkCursor parses the cursor and connected devices of an execution
// work scan; an empty cursor starts at the first ID. A malformed ID is
// sessions.ErrInvalidInput.
func ExecutionWorkCursor(after string, connectedDevices []string) (pgtype.UUID, []pgtype.UUID, error) {
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

// publicTurnLookup resolves the caller's path identifiers of a Turn or a
// Turn-scoped resource. A malformed tenant is sessions.ErrInvalidInput; a
// malformed Session or Turn ID resolves as a missing one.
func publicTurnLookup(tenant, session, turn string) (sqlc.GetTurnParams, error) {
	id, err := parseID(tenant)
	return sqlc.GetTurnParams{TenantID: id, SessionID: pgunit.PathID(session), ID: pgunit.PathID(turn)}, err
}
