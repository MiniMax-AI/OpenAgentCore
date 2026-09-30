package sessionpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
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
	return EnvironmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

// LoadSessionEnvironment reads, on q, the Environment of the tenant's Session
// that was not publicly deleted, or sessions.ErrNotFound. A malformed ID is
// sessions.ErrInvalidInput. Other adapters read the Environment of their
// operation with it.
func LoadSessionEnvironment(ctx context.Context, q *sqlc.Queries, tenant, session string) (sessions.Environment, error) {
	tenantID, err := parseID(tenant)
	if err != nil {
		return sessions.Environment{}, err
	}
	id, err := parseID(session)
	if err != nil {
		return sessions.Environment{}, err
	}
	row, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenantID, ID: id})
	return EnvironmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

// CreateSessionEnvironment creates, on q, the Environment of a new Session
// whose configuration has one, as sessions.CreatesEnvironment decides. Its
// preparation is pending when the Session froze a setup or initial files, and
// complete otherwise, so it runs after those are saved.
func CreateSessionEnvironment(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	return q.CreateEnvironment(ctx, sqlc.CreateEnvironmentParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session})
}

func (s *Store) GetEnvironment(ctx context.Context, tenant, environment string) (sessions.Environment, error) {
	return LoadEnvironment(ctx, s.units.Queries(), tenant, environment)
}

func (s *Store) GetSessionEnvironment(ctx context.Context, tenant, session string) (sessions.Environment, error) {
	return LoadSessionEnvironment(ctx, s.units.Queries(), tenant, session)
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
			EnvironmentID: optionalID(row.ID), SessionID: optionalID(row.SessionID), TenantID: optionalID(row.TenantID),
			DeviceID: optionalID(row.DeviceID), State: row.Initialization, Engine: row.Engine,
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
