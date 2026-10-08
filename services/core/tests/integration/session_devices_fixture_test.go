package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// agentHost is a Runtime registered as Core's startup registers the
// deployment's agent host, the Runtime Sessions are placed on.
type agentHost struct{ ID, Credential string }

// registerAgentHost registers an agent host. Tests on the shared database pass
// their tenant: a deployment-wide host would let the test's Worker list and
// place every other test's Sessions, so the host serves only that tenant, as
// the test's own deployment. A test on an isolated database passes "".
func registerAgentHost(t testing.TB, s *Store, tenant string) agentHost {
	t.Helper()
	host := agentHost{ID: uuid.NewString(), Credential: uuid.NewString()}
	if err := sessionAdapter(s).RegisterAgentHost(t.Context(), host.ID, runtimedevice.HashCredential(host.Credential)); err != nil {
		t.Fatal(err)
	}
	if tenant != "" {
		if _, err := s.pool.Exec(t.Context(), `UPDATE devices SET tenant_id = $2 WHERE id = $1`, host.ID, tenant); err != nil {
			t.Fatal(err)
		}
	}
	return host
}

// assignSession places the Session on the agent host as a Worker's placement
// does, without the execution lease a running Worker holds. A Session already
// placed elsewhere fails the test.
func assignSession(t testing.TB, s *Store, session, host string) {
	t.Helper()
	var runtime string
	err := s.pool.QueryRow(t.Context(), `INSERT INTO session_runtime_assignments (session_id, runtime_id) VALUES ($1, $2)
		ON CONFLICT (session_id) DO UPDATE SET runtime_id = session_runtime_assignments.runtime_id RETURNING runtime_id::text`, session, host).Scan(&runtime)
	if err != nil || runtime != host {
		t.Fatalf("Session placed on %q, want %s: %v", runtime, host, err)
	}
}

// bindSessionDevice binds the agent host to the Session through the
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
