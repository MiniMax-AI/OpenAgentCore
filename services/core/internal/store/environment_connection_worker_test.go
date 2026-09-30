package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentConnectionWorkerReconcilesAndReleasesLease(t *testing.T) {
	s, db := newTestStoreDB(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "connection-worker", Configuration: []byte(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := fixtureSessionStore(db).GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := executionOwner(t, db, s)
	generation := uuid.NewString()
	if err := owner.Sessions.ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, generation); err != nil {
		t.Fatal(err)
	}
	if err := owner.Sessions.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 1, true); err != nil {
		t.Fatal(err)
	}
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, db.pool)
	if err := owner.Lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	worker := startWorker(t, t.Context(), db, &execution.Dispatcher{Store: s, Registry: runtimegateway.NewRegistry()})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() { defer close(exited); done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			t.Error("worker cleanup did not exit")
		}
	})
	awaitEnvironmentConnectionState(t, ctx, db.pool, tenant, environment.ID, "disconnected")

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not close")
	}
	awaitEnvironmentConnectionState(t, t.Context(), db.pool, tenant, environment.ID, "disconnected")
	if err := worker.CheckOwnership(t.Context()); err == nil {
		t.Fatal("worker retained lease")
	}
	if len(retainedEnvironmentEvents(t, t.Context(), s, tenant, session.ID, environment.ID)) != 2 {
		t.Fatal("worker lifecycle did not retain all snapshots")
	}
}
