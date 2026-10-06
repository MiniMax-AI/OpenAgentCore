package sessionpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (e *Execution) WithInitialization(ctx context.Context, tenant, session string, apply func(context.Context, sessions.InitializationTx, sessions.LockedSession) error) error {
	tenantID, err := parseID(tenant)
	if err != nil {
		return err
	}
	sessionID := pgunit.PathID(session)
	return WithSession(ctx, e.lease, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		return apply(ctx, &environmentTx{SessionTx: BindSession(q, tenantID, sessionID)}, locked)
	})
}

// WithConnection reads the Environment in the transaction before it locks
// the Environment's Session.
func (e *Execution) WithConnection(ctx context.Context, tenant, environment string, apply func(context.Context, sessions.ConnectionTx, sessions.LockedSession) error) error {
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		current, err := LoadEnvironment(ctx, q, tenant, environment)
		if err != nil {
			return err
		}
		lookup, err := DeviceLookup(tenant, current.SessionID)
		if err != nil {
			return err
		}
		return inSession(ctx, q, lookup.TenantID, lookup.ID, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
			return apply(ctx, &environmentTx{SessionTx: BindSession(q, lookup.TenantID, lookup.ID)}, locked)
		})
	})
}

func (e *Execution) WithDeviceBinding(ctx context.Context, tenant, session string, apply func(context.Context, sessions.DeviceBindingTx, sessions.LockedSession) error) error {
	lookup, err := DeviceLookup(tenant, session)
	if err != nil {
		return err
	}
	return WithSession(ctx, e.lease, lookup.TenantID, lookup.ID, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		return apply(ctx, BindSession(q, lookup.TenantID, lookup.ID), locked)
	})
}

func (e *Execution) ListEnvironmentConnections(ctx context.Context, after string) ([]sessions.EnvironmentKey, error) {
	id, err := pageAfter(after)
	if err != nil {
		return nil, err
	}
	var rows []sqlc.ListEnvironmentConnectionsRow
	err = e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListEnvironmentConnections(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	keys := make([]sessions.EnvironmentKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, sessions.EnvironmentKey{TenantID: optionalID(row.TenantID), EnvironmentID: optionalID(row.ID)})
	}
	return keys, nil
}

// environmentTx is the Session's Environment inside the Session transaction
// of an initialization or connection operation.
type environmentTx struct{ *SessionTx }

var (
	_ sessions.InitializationTx = (*environmentTx)(nil)
	_ sessions.ConnectionTx     = (*environmentTx)(nil)
)

func (t *environmentTx) LoadSessionDevice(ctx context.Context) (sessions.ExecutionDevice, bool, error) {
	return loadSessionDevice(ctx, t.q, t.tenant, t.session)
}

func (t *environmentTx) ClaimInitialization(ctx context.Context, environment string) (bool, error) {
	id, err := parseID(environment)
	if err != nil {
		return false, err
	}
	count, err := t.q.ClaimEnvironmentInitialization(ctx, id)
	return count == 1, err
}

func (t *environmentTx) CompleteInitialization(ctx context.Context, environment string) (bool, error) {
	id, err := parseID(environment)
	if err != nil {
		return false, err
	}
	count, err := t.q.CompleteEnvironmentInitialization(ctx, id)
	return count == 1, err
}

func (t *environmentTx) FailInitialization(ctx context.Context, environment string) error {
	id, err := parseID(environment)
	if err != nil {
		return err
	}
	return t.q.FailEnvironmentInitialization(ctx, id)
}

func (t *environmentTx) LoadConnection(ctx context.Context, environment string) (sessions.EnvironmentConnection, bool, error) {
	id, err := parseID(environment)
	if err != nil {
		return sessions.EnvironmentConnection{}, false, err
	}
	row, err := t.q.GetEnvironmentConnection(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.EnvironmentConnection{}, false, nil
	}
	if err != nil {
		return sessions.EnvironmentConnection{}, false, err
	}
	return sessions.EnvironmentConnection{Generation: uuid.UUID(row.Generation.Bytes).String(), Revision: row.Revision}, true, nil
}

func (t *environmentTx) ReplaceConnection(ctx context.Context, environment, generation string) error {
	id, err := parseID(environment)
	if err != nil {
		return err
	}
	current, err := parseID(generation)
	if err != nil {
		return err
	}
	return t.q.ReplaceEnvironmentConnection(ctx, sqlc.ReplaceEnvironmentConnectionParams{EnvironmentID: id, Generation: current})
}

func (t *environmentTx) AdvanceConnection(ctx context.Context, environment string, revision int64) error {
	id, err := parseID(environment)
	if err != nil {
		return err
	}
	return t.q.AdvanceEnvironmentConnection(ctx, sqlc.AdvanceEnvironmentConnectionParams{EnvironmentID: id, Revision: revision})
}

func (t *environmentTx) DeleteConnection(ctx context.Context, environment string) error {
	id, err := parseID(environment)
	if err != nil {
		return err
	}
	return t.q.DeleteEnvironmentConnection(ctx, id)
}

func (t *environmentTx) SetConnectionStatus(ctx context.Context, environment, status string) error {
	id, err := parseID(environment)
	if err != nil {
		return err
	}
	return t.q.SetEnvironmentConnectionStatus(ctx, sqlc.SetEnvironmentConnectionStatusParams{ID: id, Status: status})
}
