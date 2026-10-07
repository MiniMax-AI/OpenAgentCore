package deploymentpg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (e *Execution) WithSessionArchive(ctx context.Context, tenantID, sessionID string, apply func(context.Context, sessions.LockedSession, deployment.SessionArchiveTx) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	session := pgunit.PathID(sessionID)
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		locked, err := sessionpg.LockSession(ctx, q, tenant, session)
		if err != nil {
			return err
		}
		t := &archiveTx{SessionTx: sessionpg.BindSession(q, tenant, session), unit: unit{ctx: ctx, q: q, locked: true}, tenantID: tenantID, tenant: tenant, session: session}
		if err := apply(ctx, locked, t); err != nil {
			return err
		}
		return sessionpg.PruneChanges(ctx, q, session)
	})
}

// archiveTx is one Session archive. Its unit locks the deployment when it
// reads it; tenantID is the caller's tenant as the audit records it.
type archiveTx struct {
	*sessionpg.SessionTx
	unit
	tenantID        string
	tenant, session pgtype.UUID
}

func (t *archiveTx) LoadResetBusy() (bool, error) {
	return t.q.SessionBlocksAutoReset(t.ctx, t.session)
}

func (t *archiveTx) LoadProject() (string, error) {
	project, err := t.q.GetSandboxResetProject(t.ctx, t.tenant)
	return uuidString(project), err
}

func (t *archiveTx) FindAllocation(environment string) (deployment.Allocation, bool, error) {
	id, err := parseID(environment)
	if err != nil {
		return deployment.Allocation{}, false, err
	}
	return findAllocation(t.ctx, t.q, t.tenant, id)
}

func (t *archiveTx) RequestArchiveCleanup(current deployment.Allocation) error {
	device, err := parseID(current.DeviceID)
	if err != nil {
		return err
	}
	id, err := parseID(current.ID)
	if err != nil {
		return err
	}
	if err := t.ReleaseAssignment(t.ctx, false); err != nil {
		return err
	}
	if _, err := t.q.RevokeArchivedRuntimeDevice(t.ctx, sqlc.RevokeArchivedRuntimeDeviceParams{TenantID: t.tenant, DeviceID: device, SessionID: t.session}); err != nil {
		return err
	}
	_, err = t.q.RequestRuntimeCleanup(t.ctx, id)
	return err
}

func (t *archiveTx) ReleasePlacement() error {
	return t.q.ReleaseUnallocatedRuntimePlacement(t.ctx, t.session)
}

func (t *archiveTx) RecordArchiveAudit(ctx context.Context) error {
	return auditpg.RecordAdminMutation(ctx, t.q, t.tenantID, "archive", "session", uuidString(t.session))
}
