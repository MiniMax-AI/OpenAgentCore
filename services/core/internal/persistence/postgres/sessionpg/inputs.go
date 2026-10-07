package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var (
	_ sessions.InputTx     = (*SessionTx)(nil)
	_ sessions.InputReader = (*Store)(nil)
)

func (s *Store) WithInputs(ctx context.Context, tenant, session string, apply func(context.Context, sessions.InputTx) error) error {
	return withInputs(ctx, s.units, tenant, session, apply)
}

// withInputs runs apply on runner in the Session transaction of the tenant's
// visible Session. A malformed tenant is sessions.ErrInvalidInput; a malformed
// Session ID resolves as a missing Session.
func withInputs(ctx context.Context, runner pgunit.Transactor, tenantID, sessionID string, apply func(context.Context, sessions.InputTx) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	session := pgunit.PathID(sessionID)
	return WithSession(ctx, runner, tenant, session, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		return apply(ctx, BindSession(q, tenant, session))
	})
}

func (t *SessionTx) CreateTurn(ctx context.Context) (sessions.Turn, error) {
	row, err := t.q.CreateTurn(ctx, sqlc.CreateTurnParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: t.session})
	if err != nil {
		return sessions.Turn{}, err
	}
	return TurnFromRow(row), nil
}

func (t *SessionTx) CreateTurnInput(ctx context.Context, turn, key string, position int32, input sessions.Input) (int64, error) {
	var id pgtype.UUID
	if turn != "" {
		var err error
		if id, err = parseID(turn); err != nil {
			return 0, err
		}
	}
	sequence, err := t.q.CreateTurnInput(ctx, sqlc.CreateTurnInputParams{
		SessionID: t.session, TurnID: id, IdempotencyKey: key, Kind: input.Kind, Payload: input.Payload, BatchPosition: position,
	})
	return sequence, storable(err)
}

func (t *SessionTx) LoadInputBatch(ctx context.Context, key string, batch json.RawMessage) ([]sessions.InputReceipt, bool, error) {
	rows, err := t.q.FindInputBatch(ctx, sqlc.FindInputBatchParams{SessionID: t.session, IdempotencyKey: key, Batch: batch})
	if err != nil {
		return nil, false, storable(err)
	}
	receipts := make([]sessions.InputReceipt, 0, len(rows))
	matches := true
	for _, row := range rows {
		matches = matches && row.Matches
		receipts = append(receipts, sessions.InputReceipt{Sequence: row.Sequence, TurnID: uuidString(row.TurnID), Replayed: true})
	}
	return receipts, matches, nil
}

func (t *SessionTx) LoadInputGate(ctx context.Context, key string, batch json.RawMessage) (sessions.InputGate, error) {
	gate, err := t.q.CheckEnvironmentInputGate(ctx, sqlc.CheckEnvironmentInputGateParams{SessionID: t.session, IdempotencyKey: key, Batch: batch})
	return sessions.InputGate{Matches: gate.Matches, Blocked: gate.Blocked}, storable(err)
}

func (t *SessionTx) FindInputReservation(ctx context.Context, key string, batch json.RawMessage) (*sessions.EnvironmentInputReservation, bool, error) {
	row, err := t.q.FindEnvironmentInputReservation(ctx, sqlc.FindEnvironmentInputReservationParams{SessionID: t.session, IdempotencyKey: key, Batch: batch})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, storable(err)
	}
	if !row.Matches {
		return nil, true, nil
	}
	reservation, err := t.reservationFromRow(ctx, row.EnvironmentInputReservation)
	if err != nil {
		return nil, false, err
	}
	return &reservation, true, nil
}

func (t *SessionTx) LoadInputReservation(ctx context.Context, reservation string) (sessions.EnvironmentInputReservation, error) {
	id, err := parseID(reservation)
	if err != nil {
		return sessions.EnvironmentInputReservation{}, err
	}
	row, err := t.q.GetEnvironmentInputReservation(ctx, sqlc.GetEnvironmentInputReservationParams{SessionID: t.session, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.EnvironmentInputReservation{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.EnvironmentInputReservation{}, err
	}
	return t.reservationFromRow(ctx, row)
}

// reservationFromRow maps a stored Environment input reservation; an
// admitted one carries the receipts of its batch, which it must have.
func (t *SessionTx) reservationFromRow(ctx context.Context, row sqlc.EnvironmentInputReservation) (sessions.EnvironmentInputReservation, error) {
	result := sessions.EnvironmentInputReservation{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), Key: row.IdempotencyKey,
		State: row.State, IsInitial: row.IsInitial, CreatedAt: row.CreatedAt.Time, Deadline: row.Deadline.Time,
	}
	if row.SettledAt.Valid {
		result.SettledAt = &row.SettledAt.Time
	}
	if err := json.Unmarshal(row.Batch, &result.Inputs); err != nil {
		return sessions.EnvironmentInputReservation{}, fmt.Errorf("decode Environment input: %w", err)
	}
	if row.State != sessions.EnvironmentInputAdmitted {
		return result, nil
	}
	receipts, matches, err := t.LoadInputBatch(ctx, row.IdempotencyKey, row.Batch)
	if err != nil {
		return sessions.EnvironmentInputReservation{}, err
	}
	if len(receipts) == 0 || !matches {
		return sessions.EnvironmentInputReservation{}, errors.New("admitted Environment input has no receipts of its batch")
	}
	result.Receipts = receipts
	return result, nil
}

func (t *SessionTx) CreateInputReservation(ctx context.Context, key string, batch json.RawMessage, initial bool) (sessions.EnvironmentInputReservation, error) {
	row, err := t.q.CreateEnvironmentInputReservation(ctx, sqlc.CreateEnvironmentInputReservationParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: t.session, IdempotencyKey: key, Batch: batch, IsInitial: initial,
	})
	if err != nil {
		return sessions.EnvironmentInputReservation{}, storable(err)
	}
	return t.reservationFromRow(ctx, row)
}

func (t *SessionTx) ExpireInputReservation(ctx context.Context, reservation string) error {
	id, err := parseID(reservation)
	if err != nil {
		return err
	}
	return t.q.ExpireEnvironmentInputReservation(ctx, sqlc.ExpireEnvironmentInputReservationParams{SessionID: t.session, ID: id})
}

func (t *SessionTx) AdmitInputReservation(ctx context.Context, reservation string) (time.Time, error) {
	id, err := parseID(reservation)
	if err != nil {
		return time.Time{}, err
	}
	row, err := t.q.SettleEnvironmentInputReservation(ctx, sqlc.SettleEnvironmentInputReservationParams{SessionID: t.session, ID: id, State: sessions.EnvironmentInputAdmitted})
	if err != nil {
		return time.Time{}, err
	}
	return row.SettledAt.Time, nil
}

func (t *SessionTx) FailInputReservation(ctx context.Context, reservation, code string) error {
	id, err := parseID(reservation)
	if err != nil {
		return err
	}
	_, err = t.q.FailEnvironmentInput(ctx, sqlc.FailEnvironmentInputParams{SessionID: t.session, ID: id, FailureCode: pgtype.Text{String: code, Valid: true}})
	return err
}

func (t *SessionTx) RecordInputAudit(ctx context.Context) error {
	return auditpg.RecordWriteAudit(ctx, t.q, optionalID(t.tenant), "send_events", "session", optionalID(t.session), "")
}

func (s *Store) ListTurnInputs(ctx context.Context, tenant, session, turn string, after int64, limit int) ([]sessions.TurnInput, error) {
	params, err := TurnLookup(tenant, session, turn)
	if err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: nonnegative cursor and page size 1..100 required", sessions.ErrInvalidInput)
	}
	if _, err := s.GetTurn(ctx, tenant, session, turn); err != nil {
		return nil, err
	}
	rows, err := s.units.Queries().ListTurnInputs(ctx, sqlc.ListTurnInputsParams{
		TenantID: params.TenantID, SessionID: params.SessionID, TurnID: params.ID, Sequence: after, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list turn inputs: %w", err)
	}
	inputs := make([]sessions.TurnInput, 0, len(rows))
	for _, row := range rows {
		inputs = append(inputs, sessions.TurnInput{Sequence: row.Sequence, Kind: row.Kind, Payload: row.Payload, CreatedAt: row.CreatedAt.Time})
	}
	return inputs, nil
}

func (s *Store) GetEnvironmentInputReservation(ctx context.Context, tenantID, sessionID, reservationID string) (sessions.EnvironmentInputReservation, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.EnvironmentInputReservation{}, err
	}
	session := pgunit.PathID(sessionID)
	var reservation sessions.EnvironmentInputReservation
	err = s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := visibleSession(ctx, q, tenant, session); err != nil {
			return err
		}
		var err error
		reservation, err = BindSession(q, tenant, session).LoadInputReservation(ctx, reservationID)
		return err
	})
	return reservation, err
}

func (s *Store) ListEnvironmentInputWork(ctx context.Context, after string, connectedDevices []string) ([]sessions.EnvironmentInputWork, error) {
	id, devices, err := executionWorkCursor(after, connectedDevices)
	if err != nil {
		return nil, err
	}
	rows, err := s.units.Queries().ListEnvironmentInputWork(ctx, sqlc.ListEnvironmentInputWorkParams{AfterID: id, ConnectedDevices: devices})
	if err != nil {
		return nil, err
	}
	work := make([]sessions.EnvironmentInputWork, 0, len(rows))
	for _, row := range rows {
		work = append(work, sessions.EnvironmentInputWork{TenantID: uuid.UUID(row.TenantID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), ReservationID: uuid.UUID(row.ID.Bytes).String()})
	}
	return work, nil
}
