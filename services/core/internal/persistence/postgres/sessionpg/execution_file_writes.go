package sessionpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// WithFileWriteReservation reads the Environment in the transaction before it
// locks the Environment's Session.
func (e *Execution) WithFileWriteReservation(ctx context.Context, tenant, environment string, apply func(context.Context, sessions.FileWriteReservationTx, sessions.Environment, sessions.LockedSession) error) error {
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		owner, err := LoadEnvironment(ctx, q, tenant, environment)
		if err != nil {
			return err
		}
		tenantID, err := parseID(tenant)
		if err != nil {
			return err
		}
		environmentID, err := parseID(owner.ID)
		if err != nil {
			return err
		}
		sessionID, err := parseID(owner.SessionID)
		if err != nil {
			return err
		}
		return inSession(ctx, q, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
			return apply(ctx, newFileWriteTx(q, tenantID, sessionID, environmentID), owner, locked)
		})
	})
}

// WithFileWriteSettlement reads the write in the transaction before it locks
// the Session of the write's Environment, whether or not that Session was
// publicly deleted. A malformed ID is sessions.ErrInvalidInput and an unknown
// write sessions.ErrNotFound.
func (e *Execution) WithFileWriteSettlement(ctx context.Context, tenant, environment, write string, apply func(context.Context, sessions.FileWriteSettlementTx) error) error {
	tenantID, err := parseID(tenant)
	if err != nil {
		return err
	}
	environmentID, err := parseID(environment)
	if err != nil {
		return err
	}
	writeID, err := parseID(write)
	if err != nil {
		return err
	}
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.GetEnvironmentFileWrite(ctx, sqlc.GetEnvironmentFileWriteParams{TenantID: tenantID, EnvironmentID: environmentID, ID: writeID})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		return inSession(ctx, q, tenantID, row.SessionID, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
			return apply(ctx, newFileWriteTx(q, tenantID, row.SessionID, environmentID))
		})
	})
}

// fileWriteTx is the file writes to the Session's Environment inside the
// Session transaction.
type fileWriteTx struct {
	environmentTx
	environment pgtype.UUID
}

var (
	_ sessions.FileWriteReservationTx = (*fileWriteTx)(nil)
	_ sessions.FileWriteSettlementTx  = (*fileWriteTx)(nil)
)

func newFileWriteTx(q *sqlc.Queries, tenant, session, environment pgtype.UUID) *fileWriteTx {
	return &fileWriteTx{environmentTx: environmentTx{SessionTx: BindSession(q, tenant, session)}, environment: environment}
}

func (t *fileWriteTx) LoadFileWrite(ctx context.Context, write string) (sessions.EnvironmentFileWrite, bool, error) {
	row, err := t.loadFileWrite(ctx, write)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.EnvironmentFileWrite{}, false, nil
	}
	if err != nil {
		return sessions.EnvironmentFileWrite{}, false, err
	}
	return fileWriteFromRow(row.EnvironmentFileWrite, row.SessionID), true, nil
}

func (t *fileWriteTx) loadFileWrite(ctx context.Context, write string) (sqlc.GetEnvironmentFileWriteRow, error) {
	id, err := parseID(write)
	if err != nil {
		return sqlc.GetEnvironmentFileWriteRow{}, err
	}
	return t.q.GetEnvironmentFileWrite(ctx, sqlc.GetEnvironmentFileWriteParams{TenantID: t.tenant, EnvironmentID: t.environment, ID: id})
}

func (t *fileWriteTx) LoadPendingInput(ctx context.Context) (bool, error) {
	return t.q.EnvironmentFileWriteHasPendingInput(ctx, t.session)
}

func (t *fileWriteTx) CreateFileWrite(ctx context.Context, key sessions.FileWriteIdentity) (sessions.EnvironmentFileWrite, error) {
	id, err := parseID(key.ID)
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	device, err := parseID(key.DeviceID)
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	var origin []byte
	if source, ok := writeaudit.FromContext(ctx); ok {
		if origin, err = json.Marshal(source); err != nil {
			return sessions.EnvironmentFileWrite{}, err
		}
	}
	row, err := t.q.CreateEnvironmentFileWrite(ctx, sqlc.CreateEnvironmentFileWriteParams{
		ID: id, EnvironmentID: t.environment, DeviceID: device, RequestSha256: key.RequestSHA256, AuditSource: origin,
	})
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	return fileWriteFromRow(row, t.session), nil
}

func (t *fileWriteTx) SettleFileWrite(ctx context.Context, write, state string) (sessions.EnvironmentFileWrite, error) {
	id, err := parseID(write)
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	row, err := t.q.SettleEnvironmentFileWrite(ctx, sqlc.SettleEnvironmentFileWriteParams{EnvironmentID: t.environment, ID: id, State: state})
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	return fileWriteFromRow(row, t.session), nil
}

func (t *fileWriteTx) RecordFileWriteAudit(ctx context.Context, write string) error {
	row, err := t.loadFileWrite(ctx, write)
	if err != nil {
		return err
	}
	if len(row.EnvironmentFileWrite.AuditSource) == 0 {
		return nil
	}
	var source writeaudit.Source
	if err := json.Unmarshal(row.EnvironmentFileWrite.AuditSource, &source); err != nil {
		return err
	}
	return auditpg.RecordWriteAudit(writeaudit.WithSource(ctx, source), t.q, optionalID(t.tenant), "upload_file", "environment", optionalID(t.environment), optionalID(t.session))
}

func fileWriteFromRow(row sqlc.EnvironmentFileWrite, session pgtype.UUID) sessions.EnvironmentFileWrite {
	result := sessions.EnvironmentFileWrite{
		Identity:      sessions.FileWriteIdentity{ID: optionalID(row.ID), DeviceID: optionalID(row.DeviceID), RequestSHA256: row.RequestSha256},
		EnvironmentID: optionalID(row.EnvironmentID), SessionID: optionalID(session), State: row.State, CreatedAt: row.CreatedAt.Time,
	}
	if row.SettledAt.Valid {
		settled := row.SettledAt.Time
		result.SettledAt = &settled
	}
	return result
}
