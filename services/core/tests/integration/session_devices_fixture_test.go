package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// bindSessionDevice binds the tenant's device to the Session through the
// Session execution operations on an execution lease of its own, and returns
// once the server released that lease, so a Worker can take it next.
func bindSessionDevice(t *testing.T, s *Store, tenant, session, device string) error {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), s.pool)
	if err != nil {
		return err
	}
	released := pgtest.ObserveExecutionLeaseRelease(t, s.pool)
	operations, err := sessions.NewExecutionOperations(sessionpg.NewExecution(lease))
	if err == nil {
		err = operations.BindSessionDevice(t.Context(), tenant, session, device)
	}
	if err := errors.Join(err, lease.Close(context.Background())); err != nil {
		return err
	}
	released()
	return nil
}
