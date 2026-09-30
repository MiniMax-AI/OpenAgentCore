package store_test

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

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Controlled provider faults exercise durable recovery, not native/model acceptance.
type lifecycleProvider struct {
	mu                              sync.Mutex
	resources                       map[string]sandbox.Info
	creates, kills, gets            int
	loseCreate, absent, unavailable bool
	credentialHash                  string
	credential                      string
}

func (p *lifecycleProvider) Create(_ context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.creates++
	p.credentialHash = runtimedevice.HashCredential(b.Credential)
	p.credential = b.Credential
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
func (p *lifecycleProvider) RunCommand(context.Context, sandbox.Reference, sandbox.Command) (sandbox.CommandResult, error) {
	return sandbox.CommandResult{}, errors.New("not used")
}

func managedWorker(t *testing.T, s *store.Store, db fixtureDB, key string, p sandbox.SandboxProvider) (*execution.Worker, func()) {
	t.Helper()
	return managedWorkerMode(t, s, db, key, p, false)
}

func managedWorkerMode(t *testing.T, s *store.Store, db fixtureDB, key string, p sandbox.SandboxProvider, maintenance bool, run ...bool) (*execution.Worker, func()) {
	t.Helper()
	registry := runtimegateway.NewRegistry()
	if peer, ok := p.(interface {
		setRuntimeGateway(*testing.T, string, *runtimegateway.Registry)
	}); ok {
		handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(s), Registry: registry})
		server := httptest.NewServer(http.HandlerFunc(handler.WS))
		t.Cleanup(server.Close)
		peer.setRuntimeGateway(t, "ws"+strings.TrimPrefix(server.URL, "http"), registry)
	}
	w := startWorker(t, t.Context(), db, &execution.Dispatcher{Store: s, Registry: registry, ManagedRuntimes: &execution.RuntimeProvider{CoreURL: "http://core.invalid/api/v1", InstallationID: key, BackendFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Provider: p, AdmissionPaused: maintenance}})
	if len(run) > 0 && run[0] {
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

func managedSession(t *testing.T, s *store.Store) (string, sessions.Session, sessions.Environment) {
	t.Helper()
	tenant := uuid.NewString()
	v, e := s.CreateSession(t.Context(), tenant, store.WithFixtureModelProvider(sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"enabled"}}}`)}))
	if e != nil {
		t.Fatal(e)
	}
	env, e := s.GetSessionEnvironment(t.Context(), tenant, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	return tenant, v, env
}

func reconcileManagedState(t *testing.T, w *execution.Worker, s *store.Store, tenant, environment, state string) {
	t.Helper()
	for range 100 {
		if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetRuntimeAllocation(t.Context(), tenant, environment)
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
	s, db := newManagedTestStoreDB(t)
	tenant, session, env := managedSession(t, s)
	key := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}, loseCreate: true}
	w, stop := managedWorker(t, s, db, key, p)
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err == nil || owner.ID == "" {
		t.Fatal("fault did not retain allocation")
	}
	credential, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID)
	if err != nil || !ok || credential.CredentialHash != p.credentialHash {
		t.Fatal("provider received unbound credential")
	}
	stop()
	next, _ := managedWorker(t, s, db, key, p)
	reconcileManagedState(t, next, s, tenant, env.ID, "running")
	recovered, err := s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
	if err != nil || recovered.ID != owner.ID || !recovered.CreateSettled || recovered.State != "running" {
		t.Fatalf("lost response recovery: %+v %v", recovered, err)
	}
	retry, err := next.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err != nil || !retry.Replayed || retry.ID != owner.ID || p.creates != 1 {
		t.Fatal("restart replayed Create")
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	// A scan may first exhaust its previous cursor before starting a new cycle.
	reconcileManagedState(t, next, s, tenant, env.ID, "released")
	clean, err := s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
	if err != nil || clean.State != "released" || p.kills != 1 {
		t.Fatalf("deleted cleanup: %+v %v", clean, err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
		t.Fatal("cleanup did not revoke authority")
	}
}

func TestManagedRuntimeUnknownCreationRetainsCleanup(t *testing.T) {
	s, db := newManagedTestStoreDB(t)
	tenant, session, env := managedSession(t, s)
	key := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}, loseCreate: true, absent: true}
	w, _ := managedWorker(t, s, db, key, p)
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err == nil {
		t.Fatal("expected uncertain creation")
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, env.ID, "cleanup_pending")
	got, err := s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
	if err != nil || got.State != "cleanup_pending" || got.CreateSettled || p.creates != 1 {
		t.Fatalf("unknown creation forgotten: %+v %v", got, err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
		t.Fatal("unknown allocation retains execution authority")
	}
	// A late completion is still owned and reclaimed on the next scan.
	p.resources[owner.ID] = sandbox.Info{Reference: sandbox.Reference{TenantID: tenant, EnvironmentID: env.ID, AllocationID: owner.ID}, ProviderID: owner.ID, State: "running", BootstrapComplete: true}
	reconcileManagedState(t, w, s, tenant, env.ID, "released")
	got, err = s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
	if err != nil || got.State != "released" || len(p.resources) != 0 {
		t.Fatalf("late creation escaped cleanup: %+v %v", got, err)
	}
}

func TestManagedRuntimeExpiryRevokesWhenProviderUnavailable(t *testing.T) {
	s, db := newManagedTestStoreDB(t)
	tenant, _, env := managedSession(t, s)
	key := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	w, _ := managedWorker(t, s, db, key, p)
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(t.Context(), "UPDATE runtime_allocations SET kept_at=clock_timestamp()-interval '61 minutes' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	p.unavailable = true
	reconcileManagedState(t, w, s, tenant, env.ID, "cleanup_pending")
	got, err := s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
	if err != nil || got.State != "cleanup_pending" {
		t.Fatalf("expiry lost on provider failure: %+v %v", got, err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
		t.Fatal("expired credential still authenticates")
	}
	if p.kills != 0 {
		t.Fatal("unavailable provider misreported cleanup")
	}
}

func TestManagedRuntimeStoppedComputeDoesNotRequestCleanup(t *testing.T) {
	s, db := newManagedTestStoreDB(t)
	tenant, _, env := managedSession(t, s)
	key := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	w, _ := managedWorker(t, s, db, key, p)
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
		got, err := s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
		if err != nil || got.State != "running" || p.kills != 0 || p.creates != 1 {
			t.Fatalf("compute interruption authorized replacement/cleanup: %+v %v", got, err)
		}
	}
}
