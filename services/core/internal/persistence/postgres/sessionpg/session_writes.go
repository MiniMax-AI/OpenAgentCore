package sessionpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ sessions.SessionStorage = (*Store)(nil)

func (s *Store) WithSessionDeletion(ctx context.Context, tenantID, sessionID string, apply func(context.Context, sessions.LockedSession, sessions.SessionDeletionTx) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	session := pgunit.PathID(sessionID)
	return WithSession(ctx, s.units, tenant, session, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		return apply(ctx, locked, &deletionTx{SessionTx: BindSession(q, tenant, session), tenantID: tenantID})
	})
}

// deletionTx is one Session deletion. tenantID is the caller's tenant as the
// write audit records it.
type deletionTx struct {
	*SessionTx
	tenantID string
}

func (t *deletionTx) ApplyDeletion(ctx context.Context) error {
	if err := t.q.DeleteSessionArtifacts(ctx, t.session); err != nil {
		return err
	}
	// Detaching starts the idle clock of a standalone Environment's compute, and
	// ends an Environment whose compute was released while the Session held it.
	environment, err := t.q.GetSessionEnvironmentID(ctx, t.session)
	if err != nil {
		return err
	}
	if err := t.q.RecordStandaloneActivity(ctx, environment); err != nil {
		return err
	}
	if err := t.q.MarkSessionDeleted(ctx, t.session); err != nil {
		return err
	}
	if err := t.q.ExpireUncomputedStandaloneEnvironment(ctx, environment); err != nil {
		return err
	}
	return t.q.ReleaseUnallocatedRuntimePlacement(ctx, t.session)
}

func (t *deletionTx) RecordDeletionAudit(ctx context.Context) error {
	return auditpg.RecordWriteAudit(ctx, t.q, t.tenantID, writeaudit.ActionDelete, writeaudit.ResourceSession, uuid.UUID(t.session.Bytes).String(), "")
}

func (s *Store) UpdateSession(ctx context.Context, tenantID, sessionID string, encoded []byte, configure sessions.ConfigureSession) (sessions.Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Session{}, err
	}
	var session sessions.Session
	err = s.units.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		current, err := q.LockVisibleSession(ctx, sqlc.LockVisibleSessionParams{TenantID: tenant, ID: pgunit.PathID(sessionID)})
		if err != nil {
			return err
		}
		configuration := current.Configuration
		if configure != nil {
			// configure sees the Session's whole snapshot; the Environment's
			// part stays with the Environment.
			snapshot, err := environmentSnapshot(ctx, q, tenant, current.ID, current.Configuration)
			if err != nil {
				return err
			}
			if snapshot, err = configure(current.Engine, snapshot); err != nil {
				return err
			}
			if configuration, _, err = sessions.SplitEnvironment(snapshot); err != nil {
				return err
			}
		}
		if encoded == nil {
			encoded = current.Metadata
		}
		row, err := q.UpdateSession(ctx, sqlc.UpdateSessionParams{TenantID: tenant, ID: current.ID, Configuration: configuration, Metadata: encoded})
		if err != nil {
			return err
		}
		if err := q.MarkSessionModelSource(ctx, sqlc.MarkSessionModelSourceParams{SessionID: row.ID, Before: current.Configuration, After: configuration}); err != nil {
			return err
		}
		if err := auditpg.RecordWriteAudit(ctx, q, tenantID, writeaudit.ActionUpdate, writeaudit.ResourceSession, uuid.UUID(row.ID.Bytes).String(), ""); err != nil {
			return err
		}
		if session, err = sessionFromRow(row); err != nil {
			return err
		}
		session, err = loadSessionActivity(ctx, q, session)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Session{}, fmt.Errorf("update session: %w", err)
	}
	return session, nil
}

func (s *Store) AuditSessionOperation(ctx context.Context, tenantID, sessionID, action string) error {
	return s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		return auditpg.RecordWriteAudit(ctx, q, tenantID, writeaudit.Action(action), writeaudit.ResourceSession, uuid.UUID(session.Bytes).String(), "")
	})
}
