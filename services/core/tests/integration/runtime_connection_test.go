package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// TestManagedRuntimeConnectionFollowsServe checks that a hosted Environment is
// connected while the relay holds its allocation's serve peer, and that a
// restarted Worker publishes it connected again.
func TestManagedRuntimeConnectionFollowsServe(t *testing.T) {
	s, key := configuredStore(t)
	tenant, session, environment := managedSession(t, s)
	link := sandboxlinktest.StartRelay(t, runtimegateway.NewLinkAuthority(sessionAdapter(s)))
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	start := func() (*execution.Worker, func()) {
		w, err := startNextWorker(t, t.Context(), s, &execution.Dispatcher{Registry: runtimegateway.NewRegistry(), Links: link.Relay, ManagedRuntimes: webRuntimes(t, s, key, p, nil)})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		stop := func() {
			cancel()
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		}
		return w, stop
	}
	w, stop := start()
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := sessionAdapter(s).GetEnvironment(t.Context(), tenant, environment.ID); err != nil || got.Status != "pending" {
		t.Fatal("compute existence connected the Environment", got.Status, err)
	}
	p.mu.Lock()
	io := p.serve
	p.mu.Unlock()
	serve := func() *linkServe {
		serve := startLinkServe(t, link, []byte(io.Credential), io.Resource.Ref())
		within(t, serve.connected)
		return serve
	}
	served := serve()
	awaitEnvironmentConnectionState(t, t.Context(), s, tenant, environment.ID, "connected")
	got, err := sessionAdapter(s).GetSession(t.Context(), tenant, session.ID)
	if err != nil || got.LastTurn != nil || got.EnvironmentInputActivity != nil {
		t.Fatal("connection fabricated native execution", err)
	}
	if _, err := sessionAdapter(s).GetEnvironment(t.Context(), uuid.NewString(), environment.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign Environment access", err)
	}
	served.stop()
	awaitEnvironmentConnectionState(t, t.Context(), s, tenant, environment.ID, "disconnected")
	serve()
	awaitEnvironmentConnectionState(t, t.Context(), s, tenant, environment.ID, "connected")
	stop()
	_, stop = start()
	t.Cleanup(stop)
	awaitEnvironmentConnectionState(t, t.Context(), s, tenant, environment.ID, "connected")
	retained, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID})
	if err != nil || retained.ID != owner.ID || p.creates != 1 {
		t.Fatal("restart replaced the allocation", err)
	}
}
