package deploymentpg

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5/pgtype"
)

func (e *Execution) WithResetEnvironment(ctx context.Context, key deployment.AllocationKey, apply func(deployment.ResetEnvironmentTx) error) error {
	tenant, environment, err := allocationKey(key)
	if err != nil {
		return err
	}
	return translate(sessionpg.WithEnvironment(ctx, e.lease, tenant, environment, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession, session pgtype.UUID) error {
		return apply(&resetEnvironmentTx{
			unit:      unit{ctx: ctx, q: q, locked: true},
			cleanupTx: &cleanupTx{allocationTx: &allocationTx{ctx: ctx, q: q, tenant: tenant, environment: environment, session: session}, SessionTx: sessionpg.BindSession(q, tenant, session)},
		})
	}))
}

type resetEnvironmentTx struct {
	unit
	*cleanupTx
}

func (t *resetEnvironmentTx) FindAllocation() (deployment.Allocation, bool, error) {
	return findAllocation(t.cleanupTx.ctx, t.cleanupTx.q, t.cleanupTx.tenant, t.cleanupTx.environment)
}

func (t *resetEnvironmentTx) RequestCleanup(current deployment.Allocation) error {
	if err := t.cleanupTx.RevokeDevice(current); err != nil {
		return err
	}
	_, err := t.cleanupTx.RequestCleanup(current)
	return err
}
