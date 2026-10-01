package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nodeIsolationProvider struct {
	*fakeSuspensionProvider
	blockMu   sync.Mutex
	blocked   map[string]bool
	mode      string
	armed     atomic.Bool
	entered   chan struct{}
	enterOnce sync.Once
	returned  atomic.Int32
	writes    atomic.Int32
}

func (p *nodeIsolationProvider) block(ctx context.Context, r sandbox.Reference, mode string) error {
	p.blockMu.Lock()
	blocked := p.blocked[r.EnvironmentID] && p.mode == mode
	p.blockMu.Unlock()
	if !p.armed.Load() || !blocked {
		return nil
	}
	p.enterOnce.Do(func() { close(p.entered) })
	<-ctx.Done()
	p.returned.Add(1)
	return ctx.Err()
}
func (p *nodeIsolationProvider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info, err := p.fakeSuspensionProvider.Create(ctx, b)
	if err == nil {
		err = p.connect(ctx, b)
	}
	return info, err
}
func (p *nodeIsolationProvider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	if err := p.block(ctx, r, "observe"); err != nil {
		return sandbox.Info{}, err
	}
	return p.fakeSuspensionProvider.GetInfo(ctx, r)
}
func (p *nodeIsolationProvider) GetCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	if err := p.block(ctx, r, "observe"); err != nil {
		return sandbox.ComputeState{}, err
	}
	return p.fakeSuspensionProvider.GetCompute(ctx, r, c)
}
func (p *nodeIsolationProvider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	return p.preparation.RunCommand(ctx, r, c)
}

type nodeIsolationFixture struct {
	t                    *testing.T
	store                *store.Store
	db                   fixtureDB
	nodes                *deployment.Service
	pool                 *pgxpool.Pool
	worker               *execution.Worker
	provider             *nodeIsolationProvider
	key, nodeA, nodeB    string
	epoch                uint64
	initializationCancel context.CancelFunc
	runCancel            context.CancelFunc
	runDone              chan error
	stopOnce             sync.Once
}

func newNodeIsolationFixture(t *testing.T, mode string) *nodeIsolationFixture {
	t.Helper()
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	s.SetPlacement(fixtureRules(t, db))
	registry := runtimegateway.NewRegistry()
	cp := &fakeSuspensionProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, computes: map[string]sandbox.ComputeState{}, snapshots: map[string]sandbox.RetainedState{}, bootstraps: map[string]sandbox.Bootstrap{}, peers: map[string]*websocket.Conn{}, registry: registry}
	p := &nodeIsolationProvider{fakeSuspensionProvider: cp, blocked: map[string]bool{}, mode: mode, entered: make(chan struct{})}
	preparationContext, cancelPreparation := context.WithCancel(t.Context())
	t.Cleanup(cancelPreparation)
	cp.preparation = &initializationPeer{t: t, apply: func(request proto.RuntimePreparePayload, data []byte) proto.RuntimePrepareResultPayload {
		if err := p.block(preparationContext, sandbox.Reference{EnvironmentID: request.EnvironmentID}, "initialize"); err != nil {
			return proto.RuntimePrepareResultPayload{Outcome: "unknown", ErrorCode: "runtime_preparation_unconfirmed"}
		}
		if request.Action != "file" || request.File.Path != "/workspace/seed" || string(data) != "retained" {
			t.Error("unexpected node initialization request")
			return proto.RuntimePrepareResultPayload{Outcome: "failed", ErrorCode: "runtime_preparation_failed"}
		}
		p.writes.Add(1)
		return completedInitialization(request, data)
	}}
	handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(fixtureSessionStore(db)), Registry: registry})
	server := httptest.NewServer(http.HandlerFunc(handler.WS))
	cp.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	t.Cleanup(func() {
		cp.mu.Lock()
		for _, peer := range cp.peers {
			peer.Close()
		}
		cp.mu.Unlock()
		server.Close()
	})
	f := &nodeIsolationFixture{initializationCancel: cancelPreparation, t: t, store: s, db: db, nodes: fixtureDeployment(t, db), pool: pool, provider: p, key: uuid.NewString(), nodeA: uuid.NewString(), nodeB: uuid.NewString()}
	// Keep restored compute awake throughout the isolation assertions.
	// The suspension setup explicitly dates its activity two minutes in the past.
	policy := &execution.RuntimeSuspensionPolicy{IdleTimeout: time.Minute, Retention: time.Hour, MaxActive: 100, MaxRetained: 100}
	f.worker = startWorker(t, t.Context(), db, &execution.Dispatcher{Store: s, Registry: registry, ManagedRuntimes: &execution.RuntimeProvider{CoreURL: "http://core.invalid/api/v1", InstallationID: f.key, BackendFingerprint: strings.Repeat("a", 64), Provider: p, ProviderKind: "microsandbox", LocalNodeID: f.nodeA, LocalCredentialSHA256: runtimedevice.HashCredential("local-credential"), LocalMaxActive: 100, LocalMaxRetained: 100, Suspension: policy}})
	spec := store.SandboxDeploymentTestSpec("microsandbox")
	raw, _ := json.Marshal(spec)
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_deployment SET specification=$1", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_nodes SET specification_digest=$1,deployment_generation=1", spec.Digest("microsandbox")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.stop)
	f.epoch = fixtureOwnerEpoch(t, db)
	f.enroll(f.nodeB)
	f.online(f.nodeA)
	f.online(f.nodeB)
	return f
}
func (f *nodeIsolationFixture) enroll(id string) {
	f.t.Helper()
	token, err := store.EnrollmentTestToken(f.nodes.CreateEnrollment(f.t.Context(), deployment.Capacity{MaxActive: 100, MaxRetained: 100}))
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = f.nodes.Enroll(f.t.Context(), token, deployment.Enrollment{DeploymentGeneration: 1, SpecificationDigest: store.SandboxDeploymentTestSpec("microsandbox").Digest("microsandbox"), NodeID: id, Credential: strings.Repeat("x", 64), Name: id, Provider: "microsandbox", BackendFingerprint: strings.Repeat("b", 64)})
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *nodeIsolationFixture) online(id string) {
	f.t.Helper()
	connection := uuid.NewString()
	if err := f.nodes.ConnectNode(f.t.Context(), id, connection, f.epoch); err != nil {
		f.t.Fatal(err)
	}
	if err := f.nodes.Heartbeat(f.t.Context(), id, connection, f.epoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
		f.t.Fatal(err)
	}
}
func (f *nodeIsolationFixture) session(node string, initialize bool) (string, sessions.Session, sessions.Environment) {
	f.t.Helper()
	tenant := uuid.NewString()
	input := sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage("{\"agent\":{\"model\":\"test\"},\"environment\":{\"type\":\"openai_hosted\"}}")}
	if initialize {
		input.InitialFiles = []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/seed", Data: []byte("retained")}}
	}
	// Placement is automatic and generation readiness is current-connection
	// authority. Do not merely change the legacy provider_ready projection.
	type presence struct {
		id, connection string
		epoch          uint64
	}
	rows, err := f.pool.Query(f.t.Context(), "SELECT id::text, connection_id::text, connected_epoch FROM runtime_nodes WHERE provider_ready AND id<>$1 AND connection_id IS NOT NULL AND removed_at IS NULL", node)
	if err != nil {
		f.t.Fatal(err)
	}
	var others []presence
	for rows.Next() {
		var value presence
		if err := rows.Scan(&value.id, &value.connection, &value.epoch); err != nil {
			rows.Close()
			f.t.Fatal(err)
		}
		others = append(others, value)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	for _, value := range others {
		if err := f.nodes.Heartbeat(f.t.Context(), value.id, value.connection, value.epoch, deployment.NodeHealth{ProviderReady: false}); err != nil {
			f.t.Fatal(err)
		}
	}
	session, err := f.store.CreateSession(f.t.Context(), tenant, input)
	for _, value := range others {
		if err := f.nodes.Heartbeat(context.WithoutCancel(f.t.Context()), value.id, value.connection, value.epoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
			f.t.Fatal(err)
		}
	}
	if err != nil {
		f.t.Fatal(err)
	}
	env, err := sessionReads(f.pool).GetSessionEnvironment(f.t.Context(), tenant, session.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	var placed string
	if err := f.pool.QueryRow(f.t.Context(), "SELECT node_id::text FROM runtime_placements WHERE environment_id=$1 AND released_at IS NULL", env.ID).Scan(&placed); err != nil || placed != node {
		f.t.Fatal("fixture did not place on its authenticated ready node", placed, node, err)
	}
	return tenant, session, env
}
func (f *nodeIsolationFixture) provision(tenant string, env sessions.Environment) deployment.Allocation {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.t.Context(), 3*time.Second)
	defer cancel()
	owner, err := f.worker.ProvisionEnvironment(ctx, tenant, env.ID, f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return owner
}
func (f *nodeIsolationFixture) phase(tenant, environment, phase string) deployment.Allocation {
	f.t.Helper()
	for range 10 {
		if err := f.worker.ReconcileManagedRuntimes(f.t.Context()); err != nil {
			f.t.Fatal(err)
		}
		owner, err := fixtureReader(f.db).EnvironmentAllocation(f.t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment})
		if err != nil {
			f.t.Fatal(err)
		}
		if owner.ComputePhase == phase {
			return owner
		}
	}
	f.t.Fatal("manual lifecycle did not reach " + phase)
	return deployment.Allocation{}
}
func (f *nodeIsolationFixture) run() {
	f.t.Helper()
	ctx, cancel := context.WithCancel(f.t.Context())
	f.runCancel, f.runDone = cancel, make(chan error, 1)
	go func() { f.runDone <- f.worker.Run(ctx) }()
}
func (f *nodeIsolationFixture) stop() {
	f.stopOnce.Do(func() {
		f.initializationCancel()
		if f.runDone == nil {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = f.worker.Run(ctx)
		} else {
			f.runCancel()
			select {
			case err := <-f.runDone:
				if err != nil && !errors.Is(err, context.Canceled) {
					f.t.Errorf("worker stopped: %v", err)
				}
			case <-time.After(5 * time.Second):
				f.t.Error("worker did not drain")
			}
		}
	})
}
func waitNodeIsolation(t *testing.T, within time.Duration, ready func() (bool, string)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		ok, state := ready()
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("healthy node did not progress while another node was blocked: %s", state)
		case <-tick.C:
		}
	}
}
