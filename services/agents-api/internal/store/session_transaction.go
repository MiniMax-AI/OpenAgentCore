package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// All Turn admission and lifecycle writes lock the tenant-scoped Session first.
// This orders inputs against completion/cancellation across service processes.
func (s *Store) withSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withSessionState(ctx, tenantID, sessionID, false, apply)
}

func (s *Store) withPublicSession(ctx context.Context, tenantID, sessionID string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withSessionState(ctx, tenantID, sessionID, true, apply)
}

func (s *Store) withSessionState(ctx context.Context, tenantID, sessionID string, public bool, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withLockedSession(ctx, tenantID, sessionID, public, func(ctx context.Context, q *sqlc.Queries, session sqlc.LockSessionRow) error {
		if public && session.DeletedAt.Valid {
			return ErrNotFound
		}
		return apply(ctx, q, session.ID)
	})
}

// withLockedSession locks the tenant-owned Session row, including a publicly
// deleted one, and commits only when apply succeeds.
func (s *Store) withLockedSession(ctx context.Context, tenantID, sessionID string, public bool, apply func(context.Context, *sqlc.Queries, sqlc.LockSessionRow) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	// Public paths resolve malformed IDs as missing; internal callers keep parseID.
	id := parsePathID(sessionID)
	if !public {
		if id, err = parseID(sessionID); err != nil {
			return err
		}
	}
	begin := func(ctx context.Context, apply func(pgx.Tx) error) error {
		return pgx.BeginFunc(ctx, s.pool, apply)
	}
	if s.executionLease != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, executionTransactionTimeout)
		defer cancel()
		begin = s.executionLease.transaction
	}
	return begin(ctx, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		session, err := q.LockSession(ctx, sqlc.LockSessionParams{TenantID: tenant, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err := apply(ctx, q, session); err != nil {
			return err
		}
		return q.PruneSessionEvents(ctx, id)
	})
}
