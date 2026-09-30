package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// ReserveEnvironmentFileWrite persists intent before external dispatch. A retry
// observes the earlier operation and never authorizes resending an unknown write.
func (s *Store) ReserveEnvironmentFileWrite(ctx context.Context, tenant, environment string, key sessions.FileWriteIdentity) (sessions.EnvironmentFileWrite, error) {
	if err := s.checkExecutionAuthority(); err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	if !key.Valid() {
		return sessions.EnvironmentFileWrite{}, sessions.ErrInvalidInput
	}
	owned, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	lookup, err := fileWriteLookup(tenant, environment, key.ID)
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	var origin []byte
	if source, ok := writeaudit.FromContext(ctx); ok {
		if err := source.Validate(tenant); err != nil {
			return sessions.EnvironmentFileWrite{}, err
		}
		origin, err = json.Marshal(source)
		if err != nil {
			return sessions.EnvironmentFileWrite{}, err
		}
	}
	var result sessions.EnvironmentFileWrite
	err = s.withPublicSession(ctx, tenant, owned.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		previous, err := q.GetEnvironmentFileWrite(ctx, lookup)
		if err == nil {
			result = fileWriteFromRow(previous.EnvironmentFileWrite, session)
			result.Replayed = true
			if result.Identity != key {
				return sessions.ErrIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		current, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: lookup.TenantID, ID: session})
		if err != nil {
			return err
		}
		if current.Environment.ID != lookup.EnvironmentID || current.Environment.Status == "failed" || current.Environment.Status == "expired" {
			return sessions.ErrInvalidInput
		}
		device, err := q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: lookup.TenantID, ID: session})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		if uuid.UUID(device.ID.Bytes).String() != key.DeviceID || device.EnvironmentID != lookup.EnvironmentID {
			return sessions.ErrDeviceBindingConflict
		}
		if err := checkRuntimeComputeAdmission(ctx, q, session); err != nil {
			return err
		}
		if err := environmentInputMayStart(ctx, q, session); err != nil {
			return err
		}
		pending, err := q.EnvironmentFileWriteHasPendingInput(ctx, session)
		if err != nil {
			return err
		}
		if pending {
			return sessions.ErrTurnConflict
		}
		deviceID, _ := parseID(key.DeviceID)
		row, err := q.CreateEnvironmentFileWrite(ctx, sqlc.CreateEnvironmentFileWriteParams{
			ID: lookup.ID, EnvironmentID: lookup.EnvironmentID, DeviceID: deviceID, RequestSha256: key.RequestSHA256, AuditSource: origin,
		})
		if err == nil {
			result = fileWriteFromRow(row, session)
		}
		return err
	})
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	return result, nil
}

// GetEnvironmentFileWrite also retains deleted-Session intents for internal
// cleanup. It is not a public resource query and never authorizes dispatch.
func (s *Store) GetEnvironmentFileWrite(ctx context.Context, tenant, environment, id string) (sessions.EnvironmentFileWrite, error) {
	lookup, err := fileWriteLookup(tenant, environment, id)
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	row, err := s.queries.GetEnvironmentFileWrite(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.EnvironmentFileWrite{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	result := fileWriteFromRow(row.EnvironmentFileWrite, row.SessionID)
	result.Replayed = true
	return result, nil
}

// SettleEnvironmentFileWrite requires an independently validated exact receipt.
// A missing receipt, cancellation or owner retirement is not a rejected upload.
func (s *Store) SettleEnvironmentFileWrite(ctx context.Context, tenant, environment string, key sessions.FileWriteIdentity, state string) (sessions.EnvironmentFileWrite, error) {
	if err := s.checkExecutionAuthority(); err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	if !key.Valid() || (state != "committed" && state != "rejected") {
		return sessions.EnvironmentFileWrite{}, sessions.ErrInvalidInput
	}
	previous, err := s.GetEnvironmentFileWrite(ctx, tenant, environment, key.ID)
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	lookup, _ := fileWriteLookup(tenant, environment, key.ID)
	var result sessions.EnvironmentFileWrite
	err = s.withSession(ctx, tenant, previous.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		current, err := q.GetEnvironmentFileWrite(ctx, lookup)
		if err != nil {
			return err
		}
		result = fileWriteFromRow(current.EnvironmentFileWrite, session)
		if result.Identity != key || (result.State != "pending" && result.State != state) {
			return sessions.ErrIdempotencyConflict
		}
		if result.State == state {
			result.Replayed = true
			return nil
		}
		row, err := q.SettleEnvironmentFileWrite(ctx, sqlc.SettleEnvironmentFileWriteParams{EnvironmentID: lookup.EnvironmentID, ID: lookup.ID, State: state})
		if err != nil {
			return err
		}
		result = fileWriteFromRow(row, session)
		if state == "committed" && len(row.AuditSource) > 0 {
			var source writeaudit.Source
			if err := json.Unmarshal(row.AuditSource, &source); err != nil {
				return err
			}
			auditCtx := writeaudit.WithSource(ctx, source)
			return auditpg.RecordWriteAudit(auditCtx, q, tenant, "upload_file", "environment", environment, uuid.UUID(session.Bytes).String())
		}
		return nil
	})
	if err != nil {
		return sessions.EnvironmentFileWrite{}, err
	}
	return result, nil
}

func fileWriteLookup(tenant, environment, id string) (sqlc.GetEnvironmentFileWriteParams, error) {
	var result sqlc.GetEnvironmentFileWriteParams
	var err error
	result.TenantID, err = parseID(tenant)
	if err == nil {
		result.EnvironmentID, err = parseID(environment)
	}
	if err == nil {
		result.ID, err = parseID(id)
	}
	return result, err
}

func fileWriteFromRow(row sqlc.EnvironmentFileWrite, session pgtype.UUID) sessions.EnvironmentFileWrite {
	result := sessions.EnvironmentFileWrite{Identity: sessions.FileWriteIdentity{ID: uuid.UUID(row.ID.Bytes).String(), DeviceID: uuid.UUID(row.DeviceID.Bytes).String(), RequestSHA256: row.RequestSha256},
		EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(), SessionID: uuid.UUID(session.Bytes).String(), State: row.State, CreatedAt: row.CreatedAt.Time}
	if row.SettledAt.Valid {
		result.SettledAt = &row.SettledAt.Time
	}
	return result
}

func checkEnvironmentFileWriteGate(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	blocked, err := q.EnvironmentFileWriteBlocksSession(ctx, session)
	if err == nil && blocked {
		return sessions.ErrTurnConflict
	}
	return err
}
