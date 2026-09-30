package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ListEnvironmentInitializations(ctx context.Context, after string) ([]sessions.EnvironmentInitialization, error) {
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
	result := make([]sessions.EnvironmentInitialization, 0, len(rows))
	for _, row := range rows {
		result = append(result, sessions.EnvironmentInitialization{EnvironmentID: runtimeUUID(row.ID), SessionID: runtimeUUID(row.SessionID), TenantID: runtimeUUID(row.TenantID), DeviceID: runtimeUUID(row.DeviceID), State: row.Initialization, Engine: row.Engine})
	}
	return result, nil
}

func (s *Store) mutateEnvironmentInitialization(ctx context.Context, owner sessions.EnvironmentInitialization, apply func(*sqlc.Queries, sqlc.GetSessionEnvironmentRow) error) error {
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
			return sessions.ErrDeviceBindingConflict
		}
		if row.Environment.Status == "failed" || row.Environment.Status == "expired" {
			return sessions.ErrNotFound
		}
		return apply(q, row)
	})
}

func (s *Store) ClaimEnvironmentInitialization(ctx context.Context, owner sessions.EnvironmentInitialization) error {
	return s.mutateEnvironmentInitialization(ctx, owner, func(q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		if err := checkInitializationDevice(ctx, q, owner, row); err != nil {
			return err
		}
		count, err := q.ClaimEnvironmentInitialization(ctx, row.Environment.ID)
		if err == nil && count != 1 {
			return sessions.ErrTurnConflict
		}
		return err
	})
}

func checkInitializationDevice(ctx context.Context, q *sqlc.Queries, owner sessions.EnvironmentInitialization, row sqlc.GetSessionEnvironmentRow) error {
	tenant, _ := parseID(owner.TenantID)
	bound, err := q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: tenant, ID: row.Environment.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrNotFound
	}
	if err != nil {
		return err
	}
	if runtimeUUID(bound.ID) != owner.DeviceID || bound.EnvironmentID != row.Environment.ID {
		return sessions.ErrTurnConflict
	}
	return nil
}

func (s *Store) CompleteEnvironmentInitialization(ctx context.Context, owner sessions.EnvironmentInitialization) error {
	return s.mutateEnvironmentInitialization(ctx, owner, func(q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		if err := checkInitializationDevice(ctx, q, owner, row); err != nil {
			return err
		}
		count, err := q.CompleteEnvironmentInitialization(ctx, row.Environment.ID)
		if err == nil && count != 1 {
			return sessions.ErrTurnConflict
		}
		return err
	})
}

// Failure records state and settles work, never destroys compute or a workspace.
func (s *Store) FailEnvironmentInitialization(ctx context.Context, owner sessions.EnvironmentInitialization, failure sessions.ProvisioningFailure) error {
	return s.mutateEnvironmentInitialization(ctx, owner, func(q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		if row.Environment.Initialization == "complete" {
			return sessions.ErrTurnConflict
		}
		if err := q.FailEnvironmentInitialization(ctx, row.Environment.ID); err != nil {
			return err
		}
		return failEnvironment(ctx, q, row, row.Environment.SessionID, failure.Reason(), failure.Detail(), func() error { return cancelSessionWork(ctx, q, row.Environment.SessionID) })
	})
}
