package sessionpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/jackc/pgx/v5"
)

// WithFileWriteReservation runs apply in the transaction of the tenant's
// Environment, with the Environment it reads under the locks.
func (e *Execution) WithFileWriteReservation(ctx context.Context, tenant, environment string, apply func(context.Context, sessions.FileWriteReservationTx, sessions.Environment, sessions.LockedSession) error) error {
	return e.withFileWrite(ctx, tenant, environment, func(ctx context.Context, tx *fileWriteTx, locked sessions.LockedSession) error {
		owner, err := tx.LoadEnvironment(ctx)
		if err != nil {
			return err
		}
		return apply(ctx, tx, owner, locked)
	})
}

// WithFileWriteSettlement runs apply in the transaction of the write's
// Environment, whether or not its Session was publicly deleted. A malformed ID
// is sessions.ErrInvalidInput and an unknown write sessions.ErrNotFound.
func (e *Execution) WithFileWriteSettlement(ctx context.Context, tenant, environment, write string, apply func(context.Context, sessions.FileWriteSettlementTx) error) error {
	if _, err := parseID(write); err != nil {
		return err
	}
	return e.withFileWrite(ctx, tenant, environment, func(ctx context.Context, tx *fileWriteTx, _ sessions.LockedSession) error {
		if _, err := tx.loadFileWrite(ctx, write); errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		} else if err != nil {
			return err
		}
		return apply(ctx, tx)
	})
}

func (e *Execution) withFileWrite(ctx context.Context, tenant, environment string, apply func(context.Context, *fileWriteTx, sessions.LockedSession) error) error {
	return e.withEnvironment(ctx, tenant, environment, func(ctx context.Context, tx *environmentTx, locked sessions.LockedSession) error {
		return apply(ctx, &fileWriteTx{environmentTx: *tx}, locked)
	})
}

// fileWriteTx is the file writes to the Environment inside its transaction.
type fileWriteTx struct{ environmentTx }

var (
	_ sessions.FileWriteReservationTx = (*fileWriteTx)(nil)
	_ sessions.FileWriteSettlementTx  = (*fileWriteTx)(nil)
)

func (t *fileWriteTx) LoadFileWrite(ctx context.Context, write string) (sessions.EnvironmentFileWrite, bool, error) {
	row, err := t.loadFileWrite(ctx, write)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.EnvironmentFileWrite{}, false, nil
	}
	if err != nil {
		return sessions.EnvironmentFileWrite{}, false, err
	}
	return fileWriteFromRow(row.EnvironmentFileWrite), true, nil
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
	return fileWriteFromRow(row), nil
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
	return fileWriteFromRow(row), nil
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
	return auditpg.RecordWriteAudit(writeaudit.WithSource(ctx, source), t.q, optionalID(t.tenant), writeaudit.ActionUploadFile, writeaudit.ResourceEnvironment, optionalID(t.environment), optionalID(t.session))
}

func fileWriteFromRow(row sqlc.EnvironmentFileWrite) sessions.EnvironmentFileWrite {
	result := sessions.EnvironmentFileWrite{
		Identity: sessions.FileWriteIdentity{ID: optionalID(row.ID), DeviceID: optionalID(row.DeviceID), RequestSHA256: row.RequestSha256},
		State:    row.State, CreatedAt: row.CreatedAt.Time,
	}
	if row.SettledAt.Valid {
		settled := row.SettledAt.Time
		result.SettledAt = &settled
	}
	return result
}
