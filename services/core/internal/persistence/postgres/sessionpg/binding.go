package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// SessionTx is one Session inside its caller's transaction. It implements the
// transaction interfaces of the sessions procedures by loading the facts they
// read and applying what they decide, and decides nothing itself.
//
// The caller guarantees what the procedures assume: the queries are bound to
// the caller's open transaction, the tenant owns the Session, the transaction
// already holds the Session lock (LockSession), and the operation that owns the
// transaction supplies its authority, such as the execution lease. A SessionTx
// never begins or commits a transaction, never chooses a pool or the lease, and
// is not used after the transaction callback it was bound in returns.
type SessionTx struct {
	q               *sqlc.Queries
	tenant, session pgtype.UUID
}

var (
	_ sessions.EnvironmentTerminationTx = (*SessionTx)(nil)
	_ sessions.InputStartTx             = (*SessionTx)(nil)
	_ sessions.ComputeAdmissionTx       = (*SessionTx)(nil)
	_ sessions.EnvironmentDeviceTx      = (*SessionTx)(nil)
)

// BindSession binds the tenant's Session to the caller's transaction-bound
// queries.
func BindSession(q *sqlc.Queries, tenant, session pgtype.UUID) *SessionTx {
	return &SessionTx{q: q, tenant: tenant, session: session}
}

func (t *SessionTx) LoadUsage(ctx context.Context) (json.RawMessage, error) {
	return LoadUsage(ctx, t.q, t.session)
}

func (t *SessionTx) AppendChanges(ctx context.Context, changes ...sessions.SessionChange) error {
	return AppendChanges(ctx, t.q, t.session, changes...)
}

func (t *SessionTx) LoadActiveTurn(ctx context.Context) (sessions.Turn, bool, error) {
	row, err := t.q.GetActiveTurn(ctx, t.session)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Turn{}, false, nil
	}
	if err != nil {
		return sessions.Turn{}, false, err
	}
	return TurnFromRow(row), true, nil
}

func (t *SessionTx) LoadTurn(ctx context.Context, turn string) (sessions.Turn, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.Turn{}, err
	}
	row, err := t.q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: t.session, ID: id})
	if err != nil {
		return sessions.Turn{}, err
	}
	return TurnFromRow(row), nil
}

func (t *SessionTx) RequestTurnCancel(ctx context.Context, turn string) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	return t.q.RequestTurnCancel(ctx, sqlc.RequestTurnCancelParams{ID: id, SessionID: t.session})
}

func (t *SessionTx) LoadEnding(ctx context.Context, turn string) (sessions.Ending, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.Ending{}, err
	}
	return LoadEnding(ctx, t.q, t.session, id)
}

func (t *SessionTx) ApplyTurnEnd(ctx context.Context, turn string, end sessions.TurnEnd) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	return ApplyTurnEnd(ctx, t.q, t.session, id, end)
}

func (t *SessionTx) CancelPendingInput(ctx context.Context) error {
	return t.q.CancelSessionEnvironmentInput(ctx, t.session)
}

func (t *SessionTx) FailPendingInput(ctx context.Context) error {
	return t.q.FailSessionEnvironmentInput(ctx, t.session)
}

func (t *SessionTx) LoadEnvironmentInput(ctx context.Context) (*sessions.EnvironmentInputState, error) {
	return LoadEnvironmentInput(ctx, t.q, t.session)
}

func (t *SessionTx) LoadEnvironment(ctx context.Context) (sessions.Environment, error) {
	row, err := t.q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: t.tenant, ID: t.session})
	return EnvironmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

// RecordEnvironmentFailure fails the Environment only while it is the
// Session's and still live; otherwise it changes nothing and returns
// pgx.ErrNoRows.
func (t *SessionTx) RecordEnvironmentFailure(ctx context.Context, environment, reason string, detail *sessions.ProvisioningFailureDetail) (time.Time, error) {
	id, err := parseID(environment)
	if err != nil {
		return time.Time{}, err
	}
	var rawDetail []byte
	if detail != nil {
		if rawDetail, err = json.Marshal(detail); err != nil {
			return time.Time{}, err
		}
	}
	failedAt, err := t.q.RecordEnvironmentFailure(ctx, sqlc.RecordEnvironmentFailureParams{
		ID: id, SessionID: t.session, TenantID: t.tenant,
		FailureReason: pgtype.Text{String: reason, Valid: true}, FailureDetail: rawDetail,
	})
	return failedAt.Time, err
}

// ExpireEnvironment expires the Environment only while it is the Session's;
// another Environment is sessions.ErrNotFound and changes nothing.
func (t *SessionTx) ExpireEnvironment(ctx context.Context, environment string) error {
	id, err := parseID(environment)
	if err != nil {
		return err
	}
	count, err := t.q.ExpireSessionEnvironment(ctx, sqlc.ExpireSessionEnvironmentParams{ID: id, SessionID: t.session, TenantID: t.tenant})
	if err == nil && count != 1 {
		return sessions.ErrNotFound
	}
	return err
}

func (t *SessionTx) LoadBoundDevice(ctx context.Context) (bool, error) {
	_, err := t.q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: t.tenant, ID: t.session})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// InsertEnvironmentDevice inserts the device only for the Session's own hosted
// Environment. The insert takes no row when the Environment already has a
// device, including one a concurrent transaction inserted first, or cannot
// take one: that is sessions.ErrDeviceBindingConflict.
func (t *SessionTx) InsertEnvironmentDevice(ctx context.Context, device sessions.ExecutionDevice, credentialHash string) error {
	id, err := parseID(device.ID)
	if err != nil {
		return err
	}
	environment, err := parseID(device.EnvironmentID)
	if err != nil {
		return err
	}
	inserted, err := t.q.CreateEnvironmentDevice(ctx, sqlc.CreateEnvironmentDeviceParams{
		ID: id, TenantID: t.tenant, SessionID: t.session, EnvironmentID: environment,
		Name: device.Name, CredentialHash: pgtype.Text{String: credentialHash, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrDeviceBindingConflict
	}
	if err != nil {
		return err
	}
	_, err = t.q.BindSessionDevice(ctx, sqlc.BindSessionDeviceParams{TenantID: t.tenant, ID: t.session, ID_2: inserted})
	return err
}

func (t *SessionTx) LoadComputeSuspension(ctx context.Context) (bool, error) {
	return t.q.RuntimeComputeBlocksAdmission(ctx, t.session)
}

func (t *SessionTx) LoadPendingFileWrite(ctx context.Context) (bool, error) {
	return t.q.EnvironmentFileWriteBlocksSession(ctx, t.session)
}
