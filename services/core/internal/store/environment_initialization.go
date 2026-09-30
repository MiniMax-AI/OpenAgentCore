package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnvironmentInitialization owns preparation independently of compute ownership.
// A running record without its process-local owner is unknown, never replayable.
type EnvironmentInitialization struct {
	EnvironmentID, SessionID, TenantID, DeviceID, State, Engine string
}

func (s *Store) ListEnvironmentInitializations(ctx context.Context, after string) ([]EnvironmentInitialization, error) {
	if err := s.checkExecutionOwnership(ctx); err != nil {
		return nil, err
	}
	id := pgtype.UUID{Valid: true}
	if after != "" {
		var err error
		id, err = parseID(after)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.queries.ListEnvironmentInitializations(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]EnvironmentInitialization, 0, len(rows))
	for _, row := range rows {
		result = append(result, EnvironmentInitialization{EnvironmentID: runtimeUUID(row.ID), SessionID: runtimeUUID(row.SessionID), TenantID: runtimeUUID(row.TenantID), DeviceID: runtimeUUID(row.DeviceID), State: row.Initialization, Engine: row.Engine})
	}
	return result, nil
}

func (s *Store) mutateEnvironmentInitialization(ctx context.Context, owner EnvironmentInitialization, apply func(*sqlc.Queries, sqlc.GetSessionEnvironmentRow) error) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	tenant, err := parseID(owner.TenantID)
	if err != nil {
		return err
	}
	return s.withPublicSession(ctx, owner.TenantID, owner.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		row, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: session})
		if err != nil {
			return err
		}
		if runtimeUUID(row.Environment.ID) != owner.EnvironmentID {
			return ErrDeviceBindingConflict
		}
		if row.Environment.Status == "failed" || row.Environment.Status == "expired" {
			return ErrNotFound
		}
		return apply(q, row)
	})
}

func (s *Store) ClaimEnvironmentInitialization(ctx context.Context, owner EnvironmentInitialization) error {
	return s.mutateEnvironmentInitialization(ctx, owner, func(q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		if err := checkInitializationDevice(ctx, q, owner, row); err != nil {
			return err
		}
		count, err := q.ClaimEnvironmentInitialization(ctx, row.Environment.ID)
		if err == nil && count != 1 {
			return ErrTurnConflict
		}
		return err
	})
}

func checkInitializationDevice(ctx context.Context, q *sqlc.Queries, owner EnvironmentInitialization, row sqlc.GetSessionEnvironmentRow) error {
	tenant, _ := parseID(owner.TenantID)
	bound, err := q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: tenant, ID: row.Environment.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if runtimeUUID(bound.ID) != owner.DeviceID || bound.EnvironmentID != row.Environment.ID {
		return ErrTurnConflict
	}
	return nil
}

func (s *Store) CompleteEnvironmentInitialization(ctx context.Context, owner EnvironmentInitialization) error {
	return s.mutateEnvironmentInitialization(ctx, owner, func(q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		if err := checkInitializationDevice(ctx, q, owner, row); err != nil {
			return err
		}
		count, err := q.CompleteEnvironmentInitialization(ctx, row.Environment.ID)
		if err == nil && count != 1 {
			return ErrTurnConflict
		}
		return err
	})
}

// Failure records state and settles work, never destroys compute or a workspace.
func (s *Store) FailEnvironmentInitialization(ctx context.Context, owner EnvironmentInitialization, failure ProvisioningFailure) error {
	return s.mutateEnvironmentInitialization(ctx, owner, func(q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		if row.Environment.Initialization == "complete" {
			return ErrTurnConflict
		}
		if err := q.FailEnvironmentInitialization(ctx, row.Environment.ID); err != nil {
			return err
		}
		return failEnvironment(ctx, q, row, row.Environment.SessionID, failure.reason(), failure.detail(), func() error { return cancelSessionWork(ctx, q, row.Environment.SessionID) })
	})
}
