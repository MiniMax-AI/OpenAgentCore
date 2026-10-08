package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Controlled provider faults exercise durable recovery, not native/model acceptance.
type lifecycleProvider struct {
	mu                              sync.Mutex
	resources                       map[string]sandbox.Info
	creates, kills, gets            int
	loseCreate, absent, unavailable bool
	serve                           sandboxbootstrap.Input
}

func (p *lifecycleProvider) Create(_ context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.creates++
	p.serve = b.SandboxIO
	i := sandbox.Info{Reference: b.Reference, ProviderID: b.AllocationID, State: "running", BootstrapComplete: true}
	if !p.absent {
		p.resources[b.AllocationID] = i
	}
	if p.loseCreate {
		return sandbox.Info{}, errors.New("fixture: lost Create response")
	}
	return i, nil
}
func (p *lifecycleProvider) GetInfo(_ context.Context, r sandbox.Reference) (sandbox.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gets++
	if p.unavailable {
		return sandbox.Info{}, errors.New("fixture: provider offline")
	}
	i, ok := p.resources[r.AllocationID]
	if !ok {
		return sandbox.Info{}, sandbox.ErrNotFound
	}
	return i, nil
}
func (p *lifecycleProvider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.GetInfo(ctx, r)
}
func (p *lifecycleProvider) Kill(_ context.Context, r sandbox.Reference) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kills++
	delete(p.resources, r.AllocationID)
	return nil
}

// managedWorker starts a Worker that runs the Web setup webDeployment
// committed for installation key on p.
func managedWorker(t *testing.T, s *Store, key string, p sandbox.SandboxProvider) (*execution.Worker, func()) {
	t.Helper()
	return managedWorkerMode(t, s, key, p, false)
}

// managedWorkerMode is managedWorker that also runs the Worker when run is set.
func managedWorkerMode(t *testing.T, s *Store, key string, p sandbox.SandboxProvider, run bool) (*execution.Worker, func()) {
	t.Helper()
	dispatcher := &execution.Dispatcher{Registry: runtimegateway.NewRegistry(), ManagedRuntimes: webRuntimes(t, s, key, p, nil)}
	if peer, ok := p.(interface {
		setRuntimeGateway(*testing.T, *Store, string, *runtimegateway.Registry, *sandboxlinktest.Server)
	}); ok {
		handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(sessionAdapter(s)), Registry: dispatcher.Registry})
		server := httptest.NewServer(http.HandlerFunc(handler.WS))
		t.Cleanup(server.Close)
		link := sandboxlinktest.StartRelay(t, runtimegateway.NewLinkAuthority(sessionAdapter(s)))
		peer.setRuntimeGateway(t, s, "ws"+strings.TrimPrefix(server.URL, "http"), dispatcher.Registry, link)
		dispatcher.Links = link.Relay
	}
	w, err := startNextWorker(t, t.Context(), s, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	if run {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			})
		}
		t.Cleanup(stop)
		return w, stop
	}
	var once sync.Once
	stop := func() {
		once.Do(func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = w.Run(ctx) })
	}
	t.Cleanup(stop)
	return w, stop
}

func managedSession(t *testing.T, s *Store) (string, sessions.Session, sessions.Environment) {
	t.Helper()
	tenant := uuid.NewString()
	v, e := s.CreateSession(t.Context(), tenant, WithFixtureModelProvider(sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"enabled"}}}`)}))
	if e != nil {
		t.Fatal(e)
	}
	env, e := sessionAdapter(s).GetSessionEnvironment(t.Context(), tenant, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	return tenant, v, env
}

func reconcileManagedState(t *testing.T, w *execution.Worker, s *Store, tenant, environment, state string) {
	t.Helper()
	for range 100 {
		if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
		got, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment})
		if err != nil {
			t.Fatal(err)
		}
		if got.State == state {
			return
		}
	}
	t.Fatal("allocation did not reach", state)
}

func TestManagedRuntimeLostCreateRestartAndDeletion(t *testing.T) {
	s, key := configuredStore(t)
	tenant, session, env := managedSession(t, s)
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}, loseCreate: true}
	w, stop := managedWorker(t, s, key, p)
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err == nil || owner.ID == "" {
		t.Fatal("fault did not retain allocation")
	}
	stop()
	next, _ := managedWorker(t, s, key, p)
	reconcileManagedState(t, next, s, tenant, env.ID, "running")
	recovered, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: env.ID})
	if err != nil || recovered.ID != owner.ID || !recovered.CreateSettled || recovered.State != "running" {
		t.Fatalf("lost response recovery: %+v %v", recovered, err)
	}
	retry, err := next.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err != nil || !retry.Replayed || retry.ID != owner.ID || p.creates != 1 {
		t.Fatal("restart replayed Create")
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	// A scan may first exhaust its previous cursor before starting a new cycle.
	reconcileManagedState(t, next, s, tenant, env.ID, "released")
	clean, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: env.ID})
	if err != nil || clean.State != "released" || p.kills != 1 {
		t.Fatalf("deleted cleanup: %+v %v", clean, err)
	}
}

func TestManagedRuntimeUnknownCreationRetainsCleanup(t *testing.T) {
	s, key := configuredStore(t)
	tenant, session, env := managedSession(t, s)
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}, loseCreate: true, absent: true}
	w, _ := managedWorker(t, s, key, p)
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err == nil {
		t.Fatal("expected uncertain creation")
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, env.ID, "cleanup_pending")
	got, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: env.ID})
	if err != nil || got.State != "cleanup_pending" || got.CreateSettled || p.creates != 1 {
		t.Fatalf("unknown creation forgotten: %+v %v", got, err)
	}
	// A late completion is still owned and reclaimed on the next scan.
	p.resources[owner.ID] = sandbox.Info{Reference: sandbox.Reference{TenantID: tenant, EnvironmentID: env.ID, AllocationID: owner.ID}, ProviderID: owner.ID, State: "running", BootstrapComplete: true}
	reconcileManagedState(t, w, s, tenant, env.ID, "released")
	got, err = deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: env.ID})
	if err != nil || got.State != "released" || len(p.resources) != 0 {
		t.Fatalf("late creation escaped cleanup: %+v %v", got, err)
	}
}

func TestManagedRuntimeStoppedComputeDoesNotRequestCleanup(t *testing.T) {
	s, key := configuredStore(t)
	tenant, _, env := managedSession(t, s)
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	w, _ := managedWorker(t, s, key, p)
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	info := p.resources[owner.ID]
	info.State = "exited"
	p.resources[owner.ID] = info
	for _, missing := range []bool{false, true} {
		if missing {
			delete(p.resources, owner.ID)
		}
		before := p.gets
		for i := 0; i < 100 && p.gets == before; i++ {
			if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if p.gets == before {
			t.Fatal("fixture allocation not inspected")
		}
		got, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: env.ID})
		if err != nil || got.State != "running" || p.kills != 0 || p.creates != 1 {
			t.Fatalf("compute interruption authorized replacement/cleanup: %+v %v", got, err)
		}
	}
}
