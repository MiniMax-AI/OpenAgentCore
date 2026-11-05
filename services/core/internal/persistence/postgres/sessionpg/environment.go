package sessionpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// LoadEnvironment reads, on q, the tenant's Environment of a Session that was
// not publicly deleted. A malformed tenant is sessions.ErrInvalidInput, and a
// malformed Environment ID a missing Environment, sessions.ErrNotFound. Other
// adapters read the Environment of their operation with it.
func LoadEnvironment(ctx context.Context, q *sqlc.Queries, tenant, environment string) (sessions.Environment, error) {
	tenantID, err := parseID(tenant)
	if err != nil {
		return sessions.Environment{}, err
	}
	row, err := q.GetEnvironment(ctx, sqlc.GetEnvironmentParams{TenantID: tenantID, ID: pgunit.PathID(environment)})
	return environmentFromRow(row.Environment, row.AttachedSessionID, row.ExternalWorkspace, err)
}

// loadSessionEnvironment reads, on q, the Environment of the tenant's Session
// that was not publicly deleted, or sessions.ErrNotFound. A malformed ID is
// sessions.ErrInvalidInput.
func loadSessionEnvironment(ctx context.Context, q *sqlc.Queries, tenant, session string) (sessions.Environment, error) {
	tenantID, err := parseID(tenant)
	if err != nil {
		return sessions.Environment{}, err
	}
	id, err := parseID(session)
	if err != nil {
		return sessions.Environment{}, err
	}
	row, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenantID, ID: id})
	return environmentFromRow(row.Environment, row.AttachedSessionID, row.ExternalWorkspace, err)
}

func (s *Store) GetEnvironment(ctx context.Context, tenant, environment string) (sessions.Environment, error) {
	return LoadEnvironment(ctx, s.units.Queries(), tenant, environment)
}

func (s *Store) GetSessionEnvironment(ctx context.Context, tenant, session string) (sessions.Environment, error) {
	return loadSessionEnvironment(ctx, s.units.Queries(), tenant, session)
}

func (s *Store) ListEnvironmentInitializations(ctx context.Context, after string) ([]sessions.EnvironmentInitialization, error) {
	id, err := pageAfter(after)
	if err != nil {
		return nil, err
	}
	rows, err := s.units.Queries().ListEnvironmentInitializations(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]sessions.EnvironmentInitialization, 0, len(rows))
	for _, row := range rows {
		result = append(result, sessions.EnvironmentInitialization{
			EnvironmentID: optionalID(row.ID), TenantID: optionalID(row.TenantID),
			DeviceID: optionalID(row.DeviceID), State: row.Initialization,
		})
	}
	return result, nil
}

// pageAfter parses the Environment a page of Environments starts after; empty
// starts from the first one.
func pageAfter(after string) (pgtype.UUID, error) {
	if after == "" {
		return pgtype.UUID{Valid: true}, nil
	}
	return parseID(after)
}

// optionalID formats a nullable identifier, empty when it is NULL.
func optionalID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

// LoadEnvironmentInput reads the Session's latest Environment input
// reservation that no Turn has admitted or superseded, with the facts of its
// Environment, and nil when there is none. sessions.InputActivity projects it.
func LoadEnvironmentInput(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (*sessions.EnvironmentInputState, error) {
	row, err := q.GetEnvironmentInputActivity(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	state := &sessions.EnvironmentInputState{
		State: row.State, Initial: row.IsInitial, CreatedAt: row.CreatedAt.Time, FailureCode: row.FailureCode.String,
		EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(), EnvironmentType: row.EnvironmentType, EnvironmentStatus: row.ConnectionStatus,
	}
	if row.SettledAt.Valid {
		state.SettledAt = row.SettledAt.Time
	}
	return state, nil
}

// ListEnvironments reads a page of the standalone Environments the creator
// made in the tenant, whatever their status, after the cursor Environment in (created, ID) order.
// It reports whether more follow. A missing cursor is sessions.ErrNotFound.
func (s *Store) ListEnvironments(ctx context.Context, tenantID string, creator identity.Subject, cursor string, limit int, ascending bool) ([]sessions.Environment, bool, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, false, err
	}
	params := sqlc.ListStandaloneEnvironmentsParams{TenantID: tenant, CreatorKind: pgtype.Text{String: creator.Kind, Valid: true}, CreatorID: pgtype.Text{String: creator.ID, Valid: true}, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	var page []sessions.Environment
	more := false
	err = s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if cursor != "" {
			after, err := q.GetStandaloneEnvironmentCursor(ctx, sqlc.GetStandaloneEnvironmentCursorParams{TenantID: tenant, ID: pgunit.PathID(cursor), CreatorKind: params.CreatorKind, CreatorID: params.CreatorID})
			if err != nil {
				return err
			}
			params.AfterCreated, params.AfterID = after.CreatedAt, after.ID
		}
		rows, err := q.ListStandaloneEnvironments(ctx, params)
		if err != nil {
			return err
		}
		if more = len(rows) > limit; more {
			rows = rows[:limit]
		}
		for _, row := range rows {
			environment, err := environmentFromRow(row.Environment, row.AttachedSessionID, row.ExternalWorkspace, nil)
			if err != nil {
				return err
			}
			page = append(page, environment)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, sessions.ErrNotFound
	}
	return page, more, err
}
