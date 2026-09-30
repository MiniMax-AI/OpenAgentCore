package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// FixtureEnvironmentDevice provisions the dedicated Runtime device of the
// tenant's hosted Environment on pool through the procedure the managed
// Runtime allocation runs, without the allocation.
func FixtureEnvironmentDevice(ctx context.Context, pool *pgxpool.Pool, tenant, environment, name, credentialHash string) (sessions.ExecutionDevice, error) {
	units := pgunit.NewPool(pool)
	current, err := sessionpg.New(units, nil).GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	registration, err := sessions.NewDeviceRegistration(name, credentialHash)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	lookup, err := sessionpg.DeviceLookup(tenant, current.SessionID)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	device := sessions.ExecutionDevice{ID: uuid.NewString(), Name: registration.Name, EnvironmentID: current.ID}
	err = sessionpg.WithSession(ctx, units, lookup.TenantID, lookup.ID, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		return sessions.CreateEnvironmentDevice(ctx, sessionpg.BindSession(q, lookup.TenantID, lookup.ID), device, registration.CredentialHash)
	})
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	return device, nil
}
