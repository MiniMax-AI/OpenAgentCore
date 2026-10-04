package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// The controlled provider records external effects independently of DB phases.
// Lost replies retain those effects so recovery must use observation, not replay.
type fakeSuspensionProvider struct {
	preparation *initializationPeer
	lifecycleProvider
	computes                                                     map[string]sandbox.ComputeState
	snapshots                                                    map[string]sandbox.RetainedState
	bootstraps                                                   map[string]sandbox.Bootstrap
	peers                                                        map[string]*websocket.Conn
	registry                                                     *runtimegateway.Registry
	endpoint                                                     string
	captures, restores, captureObservations, restoreObservations int
	computeKills, snapshotDeletes, wakeCommands                  int
	promptFrames                                                 atomic.Int32
	quiesces, resumes                                            atomic.Int32
	loseCapture, loseRestore, rejectQuiesce                      bool
	beforeQuiesce                                                func()
	renewals                                                     atomic.Int32
}

func (p *fakeSuspensionProvider) Initial(_ context.Context, r sandbox.Reference) (sandbox.Compute, error) {
	return sandbox.Compute{Name: r.AllocationID + "-g0"}, nil
}
func (p *fakeSuspensionProvider) NewCompute(_ context.Context, r sandbox.Reference, generation uint64, parent *sandbox.RetainedState) (sandbox.Compute, error) {
	return sandbox.Compute{Generation: generation, Name: fmt.Sprintf("%s-g%d", r.AllocationID, generation), RestoredFrom: parent}, nil
}
func (p *fakeSuspensionProvider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info, err := p.lifecycleProvider.Create(ctx, b)
	p.mu.Lock()
	defer p.mu.Unlock()
	current, _ := p.Initial(ctx, b.Reference)
	current.ID = uuid.NewString()
	p.computes[current.Name] = sandbox.ComputeState{Compute: current, Status: "running", BootstrapComplete: true}
	p.bootstraps[b.AllocationID] = b
	return info, err
}
func (p *fakeSuspensionProvider) GetCompute(_ context.Context, _ sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.computes[c.Name]
	if !ok {
		return sandbox.ComputeState{}, sandbox.ErrNotFound
	}
	if c.ID != "" && state.Compute.ID != c.ID {
		return sandbox.ComputeState{}, sandbox.ErrOwnership
	}
	return state, nil
}
func (p *fakeSuspensionProvider) Suspend(_ context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.computes[q.Source.Name]
	if !ok {
		if snapshot, exists := p.snapshots[q.OperationID]; exists && q.ReconcileOnly {
			p.captureObservations++
			return sandbox.ComputeState{Compute: q.Source, Status: "suspended", Retained: &snapshot, BootstrapComplete: true, ResourcesReleased: true, SuspendSettled: true}, nil
		}
		return sandbox.ComputeState{}, sandbox.ErrNotFound
	}
	if state.Compute.ID != q.Source.ID {
		return sandbox.ComputeState{}, sandbox.ErrOwnership
	}
	if q.ReconcileOnly {
		p.captureObservations++
	} else {
		p.captures++
		if _, exists := p.snapshots[q.OperationID]; exists {
			return sandbox.ComputeState{}, errors.New("capture replayed")
		}
		p.snapshots[q.OperationID] = sandbox.RetainedState{Reference: "snapshot-" + q.OperationID, ID: uuid.NewString(), Data: "verified-native-state", OperationID: q.OperationID, SourceGeneration: q.Source.Generation, SourceName: q.Source.Name, SourceID: q.Source.ID}
		state.Status = "paused"
		p.computes[q.Source.Name] = state
		if p.loseCapture {
			p.loseCapture = false
			return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
		}
	}
	if snapshot, exists := p.snapshots[q.OperationID]; exists {
		state.Retained = &snapshot
		state.SuspendSettled = true
		state.ResourcesReleased = true
		state.Status = "suspended"
		delete(p.computes, q.Source.Name)
		p.computeKills++
	}
	state.SuspendSettled = true
	return state, nil
}
func (p *fakeSuspensionProvider) Resume(_ context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if q.ReconcileOnly {
		p.restoreObservations++
		state, ok := p.computes[q.Target.Name]
		if !ok {
			return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
		}
		return state, nil
	}
	p.restores++
	if _, exists := p.computes[q.Target.Name]; exists {
		return sandbox.ComputeState{}, errors.New("restore replayed")
	}
	target := q.Target
	target.ID = uuid.NewString()
	state := sandbox.ComputeState{Compute: target, Status: "running", BootstrapComplete: true}
	p.computes[target.Name] = state
	if p.loseRestore {
		p.loseRestore = false
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	return state, nil
}
func (p *fakeSuspensionProvider) KillCompute(_ context.Context, _ sandbox.Reference, c sandbox.Compute) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.computes[c.Name]
	if !ok {
		return sandbox.ErrNotFound
	}
	if c.ID != "" && c.ID != state.Compute.ID {
		return sandbox.ErrOwnership
	}
	p.computeKills++
	delete(p.computes, c.Name)
	return nil
}
func (p *fakeSuspensionProvider) DeleteRetained(_ context.Context, _ sandbox.Reference, s sandbox.RetainedState) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	old, ok := p.snapshots[s.OperationID]
	if !ok {
		return sandbox.ErrNotFound
	}
	if old != s {
		return sandbox.ErrOwnership
	}
	p.snapshotDeletes++
	delete(p.snapshots, s.OperationID)
	return nil
}
func (p *fakeSuspensionProvider) ResumeCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	state, err := p.GetCompute(ctx, r, c)
	if err != nil {
		return state, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state.Status = "running"
	p.computes[c.Name] = state
	return state, nil
}
func (p *fakeSuspensionProvider) RunCommandCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute, command sandbox.Command) (sandbox.CommandResult, error) {
	if _, err := p.GetCompute(ctx, r, c); err != nil {
		return sandbox.CommandResult{}, err
	}
	if len(command.Args) != 8 || command.Args[0] != "oac-daemon" || command.Args[1] != "resume" || command.Args[5] != r.EnvironmentID {
		return sandbox.CommandResult{}, errors.New("unexpected wake command")
	}
	p.mu.Lock()
	p.wakeCommands++
	b := p.bootstraps[r.AllocationID]
	p.mu.Unlock()
	return sandbox.CommandResult{}, p.connect(ctx, b)
}
func (p *fakeSuspensionProvider) connect(ctx context.Context, b sandbox.Bootstrap) error {
	header := http.Header{"Authorization": []string{"Bearer " + b.Credential}}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, p.endpoint+"?device_id="+b.DeviceID+"&version="+proto.Version, header)
	if err != nil {
		return err
	}
	heartbeat, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}}})
	if err := conn.WriteJSON(heartbeat); err != nil {
		conn.Close()
		return err
	}
	p.mu.Lock()
	p.peers[b.AllocationID] = conn
	p.mu.Unlock()
	if _, err = p.registry.WaitForDevice(ctx, b.DeviceID, time.Second); err != nil {
		conn.Close()
		return err
	}
	go func() {
		defer conn.Close()
		transfer := initializationTransfer{peer: p.preparation}
		for {
			var env proto.Envelope
			if conn.ReadJSON(&env) != nil {
				return
			}
			if env.Type == proto.TypeRuntimePrepare && p.preparation != nil {
				reply, err := transfer.receive(env)
				if err != nil {
					p.preparation.t.Error(err)
					return
				}
				if conn.WriteJSON(reply) != nil {
					return
				}
				continue
			}
			if env.Type == proto.TypeDeviceShutdown {
				return
			}
			if env.Type != proto.TypeEnvironmentQuiesce && env.Type != proto.TypeEnvironmentResume {
				p.mu.Lock()
				p.promptFrames.Add(1)
				p.mu.Unlock()
				continue
			}
			var request proto.EnvironmentSuspendPayload
			if env.DecodePayload(&request) != nil {
				return
			}
			p.mu.Lock()
			reject := p.rejectQuiesce && env.Type == proto.TypeEnvironmentQuiesce
			if env.Type == proto.TypeEnvironmentQuiesce {
				p.quiesces.Add(1)
			} else {
				p.resumes.Add(1)
			}
			beforeQuiesce := p.beforeQuiesce
			p.mu.Unlock()
			if env.Type == proto.TypeEnvironmentQuiesce && beforeQuiesce != nil {
				beforeQuiesce()
			}
			kind := proto.TypeEnvironmentResumed
			if env.Type == proto.TypeEnvironmentQuiesce {
				kind = proto.TypeEnvironmentQuiesced
			}
			reply, _ := proto.NewEnvelope(kind, env.ID, proto.EnvironmentSuspendResultPayload{EnvironmentID: request.EnvironmentID, SuspendID: request.SuspendID, Accepted: !reject})
			if conn.WriteJSON(reply) != nil {
				return
			}
			if env.Type == proto.TypeEnvironmentQuiesce && !reject {
				return
			}
		}
	}()
	return nil
}

type computeLifecycleFixture struct {
	t        *testing.T
	store    *store.Store
	db       fixtureDB
	provider *fakeSuspensionProvider
	worker   *execution.Worker
	stop     func()
	key      string
	policy   execution.RuntimeSuspensionPolicy
}

func newComputeLifecycleFixture(t *testing.T, maxActive, maxRetained int) *computeLifecycleFixture {
	t.Helper()
	s, db := newManagedTestStoreDB(t)
	registry := runtimegateway.NewRegistry()
	p := &fakeSuspensionProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, computes: map[string]sandbox.ComputeState{}, snapshots: map[string]sandbox.RetainedState{}, bootstraps: map[string]sandbox.Bootstrap{}, peers: map[string]*websocket.Conn{}, registry: registry}
	handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(fixtureSessionStore(db)), Registry: registry})
	server := httptest.NewServer(http.HandlerFunc(handler.WS))
	p.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	t.Cleanup(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, peer := range p.peers {
			peer.Close()
		}
		server.Close()
	})
	f := &computeLifecycleFixture{t: t, store: s, db: db, provider: p, key: uuid.NewString(), policy: execution.RuntimeSuspensionPolicy{IdleTimeout: time.Second, Retention: time.Hour, MaxActive: maxActive, MaxRetained: maxRetained}}
	f.start()
	return f
}
func (f *computeLifecycleFixture) start() {
	t := f.t
	t.Helper()
	dispatcher := &execution.Dispatcher{Store: f.store, Registry: f.provider.registry, ManagedRuntimes: &execution.RuntimeProvider{CoreURL: "http://core.invalid/api/v1", InstallationID: f.key, BackendFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Provider: f.provider, Suspension: &f.policy}}
	// Closing the previous Worker's connection can return before PostgreSQL drops its advisory lock.
	deadline := time.Now().Add(2 * time.Second)
	var w *execution.Worker
	var err error
	for {
		w, err = startWorkerErr(t.Context(), f.db, dispatcher)
		if err == nil || !errors.Is(err, pgunit.ErrLeaseHeld) || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = w.Run(ctx) })
	}
	f.worker, f.stop = w, stop
	t.Cleanup(stop)
}
func (f *computeLifecycleFixture) sql(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.pool.Exec(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *computeLifecycleFixture) create() (string, sessions.Session, sessions.Environment, deployment.Allocation) {
	t := f.t
	t.Helper()
	tenant, session, environment := managedSession(t, f.store, f.db)
	owner, err := f.worker.ProvisionEnvironment(t.Context(), tenant, environment.ID, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.provider.mu.Lock()
	b := f.provider.bootstraps[owner.ID]
	f.provider.mu.Unlock()
	if err := f.provider.connect(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	owner = f.phase(tenant, environment.ID, "running")
	return tenant, session, environment, owner
}
func (f *computeLifecycleFixture) phase(tenant, environment, phase string) deployment.Allocation {
	f.t.Helper()
	var owner deployment.Allocation
	for range 100 {
		ctx, cancel := context.WithTimeout(f.t.Context(), 3*time.Second)
		err := f.worker.ReconcileManagedRuntimes(ctx)
		cancel()
		if err != nil {
			f.t.Fatal(err)
		}
		owner, err = fixtureReader(f.db).EnvironmentAllocation(f.t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment})
		if err != nil {
			f.t.Fatal(err)
		}
		if owner.ComputePhase == phase {
			return owner
		}
	}
	f.t.Fatalf("compute phase=%s, want %s state=%s", owner.ComputePhase, phase, owner.ComputeState)
	return owner
}
func (f *computeLifecycleFixture) complete(owner deployment.Allocation) string {
	id := uuid.NewString()
	f.sql(`INSERT INTO turns(id,session_id,status,completed_at) VALUES($1,$2,'completed',clock_timestamp()-interval '2 minutes')`, id, owner.SessionID)
	f.sql(`UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, owner.ID)
	return id
}
func (f *computeLifecycleFixture) queued(owner deployment.Allocation) string {
	id := uuid.NewString()
	f.sql(`INSERT INTO turns(id,session_id,status) VALUES($1,$2,'queued')`, id, owner.SessionID)
	return id
}

func TestRuntimeComputeLifecycleIdleSuspendAndQueuedSameSessionWake(t *testing.T) {
	f := newComputeLifecycleFixture(t, 2, 4)
	tenant, session, env, owner := f.create()
	for range 3 {
		f.phase(tenant, env.ID, "running")
	}
	if f.provider.captures != 0 {
		t.Fatal("recently-created Session suspended")
	}
	completed := f.complete(owner)
	suspended := f.phase(tenant, env.ID, "suspended")
	if f.provider.captures != 1 || f.provider.computeKills != 1 || len(f.provider.computes) != 0 || len(f.provider.snapshots) != 1 {
		t.Fatal("capture did not release source compute")
	}
	queued := f.queued(suspended)
	awake := f.phase(tenant, env.ID, "running")
	if awake.SessionID != session.ID || awake.ID != owner.ID || awake.DeviceID != owner.DeviceID || f.provider.creates != 1 || f.provider.restores != 1 || f.provider.snapshotDeletes != 1 {
		t.Fatal("wake replaced Session or replayed allocation")
	}
	var completedCount, queuedCount int
	if err := f.db.pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE id=$2 AND status='completed'),count(*) FILTER(WHERE id=$3 AND status='queued') FROM turns WHERE session_id=$1`, session.ID, completed, queued).Scan(&completedCount, &queuedCount); err != nil || completedCount != 1 || queuedCount != 1 {
		t.Fatal("wake replayed/consumed prior or next Turn", err)
	}
	if got, err := f.store.GetSession(t.Context(), tenant, session.ID); err != nil || string(got.Configuration) != string(session.Configuration) {
		t.Fatal("configuration changed during restore", err)
	}
	if f.provider.promptFrames.Load() != 0 {
		t.Fatal("lifecycle sent native execution input")
	}
}

func TestRuntimeComputeLifecycleUnusedSessionSuspendsAndExpires(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	tenant, session, env, owner := f.create()
	f.sql(`UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, owner.ID)
	suspended := f.phase(tenant, env.ID, "suspended")
	if suspended.ComputeRetainedUntil == nil || f.provider.captures != 1 || f.provider.computeKills != 1 || len(f.provider.computes) != 0 || len(f.provider.snapshots) != 1 {
		t.Fatal("unused Session did not suspend and release active compute")
	}
	var turns int
	if err := f.db.pool.QueryRow(t.Context(), `SELECT count(*) FROM turns WHERE session_id=$1`, session.ID).Scan(&turns); err != nil || turns != 0 {
		t.Fatal("unused Session gained a Turn", turns, err)
	}
	f.sql(`UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID)
	reconcileManagedState(t, f.worker, f.db, tenant, env.ID, "released")
	if len(f.provider.computes) != 0 || len(f.provider.snapshots) != 0 || f.provider.snapshotDeletes != 1 || f.provider.promptFrames.Load() != 0 {
		t.Fatal("unused Session expiry retained resources or sent execution input")
	}
	if _, err := f.store.GetSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal("resource cleanup removed Session history", err)
	}
}

func TestRuntimeComputeLifecycleLostCaptureAndRestoreObserveWithoutReplay(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	tenant, _, env, owner := f.create()
	f.complete(owner)
	f.provider.loseCapture = true
	uncertain := f.phase(tenant, env.ID, "suspending")
	if f.provider.captures != 1 || f.provider.computeKills != 0 {
		t.Fatal("uncertain capture was reclaimed early")
	}
	captureIntent := append([]byte(nil), uncertain.ComputeState...)
	f.stop()
	f.start()
	suspended := f.phase(tenant, env.ID, "suspended")
	if f.provider.captures != 1 || f.provider.captureObservations == 0 || f.provider.computeKills != 1 {
		t.Fatal("capture recovery replayed effects")
	}
	var original, mapState map[string]json.RawMessage
	_ = json.Unmarshal(captureIntent, &original)
	_ = json.Unmarshal(suspended.ComputeState, &mapState)
	if string(original["suspend_id"]) != string(mapState["suspend_id"]) {
		t.Fatal("capture operation identity changed")
	}
	f.provider.loseRestore = true
	f.queued(suspended)
	restoring := f.phase(tenant, env.ID, "restoring")
	f.stop()
	f.start()
	f.phase(tenant, env.ID, "running")
	if f.provider.restores != 1 || f.provider.restoreObservations == 0 || f.provider.creates != 1 {
		t.Fatal("restore recovery repeated native operation")
	}
	if restoring.ComputeRevision <= uncertain.ComputeRevision {
		t.Fatal("recovery lost durable progress")
	}
}

func TestRuntimeComputeLifecycleQuiesceRejectionAndUnknownIntentRollback(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	tenant, _, env, owner := f.create()
	f.complete(owner)
	f.provider.mu.Lock()
	f.provider.rejectQuiesce = true
	f.provider.mu.Unlock()
	for range 100 {
		f.phase(tenant, env.ID, "running")
		if f.provider.quiesces.Load() != 0 {
			break
		}
	}
	if f.provider.quiesces.Load() != 1 || f.provider.captures != 0 {
		t.Fatalf("quiesce rejection did not settle: requests=%d captures=%d", f.provider.quiesces.Load(), f.provider.captures)
	}
	// A restart with persisted quiescing but no acknowledgement must resume the
	// source. Inject only the durable phase, never a fake snapshot or new source.
	current, err := fixtureReader(f.db).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: env.ID})
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(current.ComputeState, &state); err != nil {
		t.Fatal(err)
	}
	state["suspend_id"] = uuid.NewString()
	raw, _ := json.Marshal(state)
	f.sql(`UPDATE runtime_allocations SET compute_phase='quiescing',compute_state=$2,compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID, raw)
	f.phase(tenant, env.ID, "running")
	if f.provider.captures != 0 || f.provider.restores != 0 || f.provider.resumes.Load() != 1 {
		t.Fatal("unknown quiesce did not roll back source")
	}
}

func TestRuntimeComputeLifecycleWakeDuringQuiesceResumesSource(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	tenant, _, env, owner := f.create()
	f.complete(owner)
	wakeResult := make(chan error, 1)
	activity := fixtureDeployment(t, f.db)
	f.provider.mu.Lock()
	f.provider.beforeQuiesce = func() {
		wakeResult <- activity.TouchActivity(t.Context(), tenant, env.ID)
	}
	f.provider.mu.Unlock()
	for range 100 {
		f.phase(tenant, env.ID, "running")
		if f.provider.resumes.Load() != 0 {
			break
		}
	}
	select {
	case err := <-wakeResult:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("quiesce did not reach competing wake")
	}
	if f.provider.resumes.Load() != 1 || f.provider.captures != 0 || f.provider.restores != 0 || f.provider.computeKills != 0 {
		t.Fatal("competing wake did not resume the original source before capture")
	}
}

func TestRuntimeComputeLifecycleSuspendedDeletionAndExpiryCleanup(t *testing.T) {
	for _, kind := range []string{"deleted", "expired"} {
		t.Run(kind, func(t *testing.T) {
			f := newComputeLifecycleFixture(t, 1, 2)
			tenant, session, env, owner := f.create()
			f.complete(owner)
			f.phase(tenant, env.ID, "suspended")
			if kind == "deleted" {
				if err := f.store.DeleteSession(t.Context(), tenant, session.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.sql(`UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID)
			}
			reconcileManagedState(t, f.worker, f.db, tenant, env.ID, "released")
			if len(f.provider.computes) != 0 || len(f.provider.snapshots) != 0 || f.provider.snapshotDeletes != 1 {
				t.Fatal("retained snapshot survived cleanup")
			}
			if _, ok, err := fixtureSessionStore(f.db).GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
				t.Fatal("cleanup retained daemon authority", err)
			}
		})
	}
}

func TestRuntimeComputeLifecycleCapacityBoundsActiveAndRetained(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	tenant, _, env, owner := f.create()
	tenant2, _, env2 := managedSession(t, f.store, f.db)
	if _, err := f.worker.ProvisionEnvironment(t.Context(), tenant2, env2.ID, f.key); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatalf("active capacity ignored: %v", err)
	}
	if f.provider.creates != 1 {
		t.Fatal("capacity rejection allocated compute")
	}
	f.complete(owner)
	f.phase(tenant, env.ID, "suspended")
	second, err := f.worker.ProvisionEnvironment(t.Context(), tenant2, env2.ID, f.key)
	if err != nil {
		t.Fatal(err)
	}
	pending := f.queued(owner)
	for range 4 {
		f.worker.ReconcileManagedRuntimes(t.Context())
	}
	first, err := fixtureReader(f.db).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: env.ID})
	if err != nil || first.ComputePhase != "suspended" || f.provider.restores != 0 {
		t.Fatal("wake exceeded active capacity", err)
	}
	f.sql(`UPDATE turns SET status='cancelled',completed_at=clock_timestamp() WHERE id=$1`, pending)
	f.provider.mu.Lock()
	b := f.provider.bootstraps[second.ID]
	f.provider.mu.Unlock()
	if err := f.provider.connect(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	second = f.phase(tenant2, env2.ID, "running")
	f.complete(second)
	f.phase(tenant2, env2.ID, "suspended")
	// Both retained allocations count even when their source VMs are gone.
	tenant3, _, env3 := managedSession(t, f.store, f.db)
	if _, err := f.worker.ProvisionEnvironment(t.Context(), tenant3, env3.ID, f.key); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatalf("retained capacity ignored: %v", err)
	}
	if f.provider.creates != 2 {
		t.Fatal("retained limit created a third allocation")
	}
}

func (p *fakeSuspensionProvider) RenewCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	p.renewals.Add(1)
	return p.GetCompute(ctx, r, c)
}

func TestRuntimeComputeProtocolUpgradeRefusesOldReceiptsBeforeCleanup(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	tenant, _, env, owner := f.create()
	f.complete(owner)
	retained := f.phase(tenant, env.ID, "suspended")
	f.stop()
	f.sql(`UPDATE runtime_allocations SET compute_state=(compute_state-'protocol_version'-'retained') || jsonb_build_object('snapshot',compute_state->'retained') WHERE id=$1`, owner.ID)
	deletes := f.provider.snapshotDeletes
	w, err := startWorkerErr(t.Context(), f.db, &execution.Dispatcher{Store: f.store, Registry: f.provider.registry, ManagedRuntimes: &execution.RuntimeProvider{CoreURL: "http://core.invalid/api/v1", InstallationID: f.key, BackendFingerprint: strings.Repeat("a", 64), Provider: f.provider, Suspension: &f.policy}})
	if err == nil || w != nil || !strings.Contains(err.Error(), "previous release") {
		t.Fatalf("incompatible state activated: %v", err)
	}
	if f.provider.snapshotDeletes != deletes || len(f.provider.snapshots) != 1 {
		t.Fatal("upgrade lost owned artifact")
	}
	var state []byte
	if err := f.db.pool.QueryRow(t.Context(), `SELECT compute_state FROM runtime_allocations WHERE id=$1`, owner.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"snapshot"`) {
		t.Fatal("old receipt was rewritten")
	}
	f.sql(`UPDATE runtime_allocations SET compute_state=$2::jsonb WHERE id=$1`, owner.ID, retained.ComputeState)
	f.start()
}

func TestRuntimeComputeRepeatedWakeStillRenewsCurrentIncarnation(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	tenant, _, env, owner := f.create()
	before := f.provider.renewals.Load()
	for range 3 {
		f.sql(`UPDATE runtime_allocations SET compute_wake_requested=true,compute_activity_at=clock_timestamp() WHERE id=$1`, owner.ID)
		f.phase(tenant, env.ID, "running")
	}
	if f.provider.renewals.Load() < before+3 {
		t.Fatal("wake requests bypassed native lease renewal")
	}
}

func TestRuntimeComputeProtocolRejectsOldDisabledAllocation(t *testing.T) {
	f := newComputeLifecycleFixture(t, 1, 2)
	_, _, _, owner := f.create()
	f.stop()
	f.sql(`UPDATE runtime_allocations SET compute_phase='disabled',compute_state='{}'::jsonb WHERE id=$1`, owner.ID)
	before := f.provider.renewals.Load()
	w, err := startWorkerErr(t.Context(), f.db, &execution.Dispatcher{Store: f.store, Registry: f.provider.registry, ManagedRuntimes: &execution.RuntimeProvider{CoreURL: "http://core.invalid/api/v1", InstallationID: f.key, BackendFingerprint: strings.Repeat("a", 64), Provider: f.provider, Suspension: &f.policy}})
	if err == nil || w != nil || !strings.Contains(err.Error(), "previous release") {
		t.Fatal("old disabled allocation activated", err)
	}
	if f.provider.renewals.Load() != before || len(f.provider.computes) != 1 {
		t.Fatal("upgrade changed owned native compute")
	}
}
