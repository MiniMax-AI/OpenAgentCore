package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nodeIsolationProvider struct {
	*fakeCheckpointProvider
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
	info, err := p.fakeCheckpointProvider.Create(ctx, b)
	if err == nil {
		err = p.connect(ctx, b)
	}
	return info, err
}
func (p *nodeIsolationProvider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	if err := p.block(ctx, r, "observe"); err != nil {
		return sandbox.Info{}, err
	}
	return p.fakeCheckpointProvider.GetInfo(ctx, r)
}
func (p *nodeIsolationProvider) GetCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	if err := p.block(ctx, r, "observe"); err != nil {
		return sandbox.ComputeState{}, err
	}
	return p.fakeCheckpointProvider.GetCompute(ctx, r, c)
}
func (p *nodeIsolationProvider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	if err := p.block(ctx, r, "initialize"); err != nil {
		return sandbox.CommandResult{}, err
	}
	size, err := strconv.Atoi(c.Args[len(c.Args)-1])
	if err != nil || len(c.Stdin) != size+32 {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	p.writes.Add(1)
	return sandbox.CommandResult{Stdout: fmt.Sprintf("{\"version\":1,\"outcome\":\"completed\",\"size_bytes\":%d}", size)}, nil
}

type nodeIsolationFixture struct {
	t                 *testing.T
	store             *store.Store
	pool              *pgxpool.Pool
	worker            *execution.Worker
	provider          *nodeIsolationProvider
	key, nodeA, nodeB string
	epoch             uint64
	runCancel         context.CancelFunc
	runDone           chan error
	stopOnce          sync.Once
}

func newNodeIsolationFixture(t *testing.T, mode string) *nodeIsolationFixture {
	t.Helper()
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	registry := gateway.NewRegistry()
	cp := &fakeCheckpointProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, computes: map[string]sandbox.ComputeState{}, snapshots: map[string]sandbox.SnapshotIdentity{}, bootstraps: map[string]sandbox.Bootstrap{}, peers: map[string]*websocket.Conn{}, registry: registry}
	p := &nodeIsolationProvider{fakeCheckpointProvider: cp, blocked: map[string]bool{}, mode: mode, entered: make(chan struct{})}
	handler := gateway.NewHandler(gateway.HandlerConfig{Authenticator: gateway.NewAuthenticator(s), Registry: registry})
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
	f := &nodeIsolationFixture{t: t, store: s, pool: pool, provider: p, key: uuid.NewString(), nodeA: uuid.NewString(), nodeB: uuid.NewString()}
	// Keep restored compute awake throughout the isolation assertions.
	// The suspension setup explicitly dates its activity two minutes in the past.
	policy := &execution.RuntimeSuspensionPolicy{IdleTimeout: time.Minute, Retention: time.Hour, MaxActive: 100, MaxRetained: 100}
	f.worker, err = execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: registry, ManagedRuntimes: &execution.RuntimeProvider{CoreURL: "http://core.invalid/api/v1", InstallationID: f.key, BackendFingerprint: strings.Repeat("a", 64), Provider: p, ProviderKind: "microsandbox", LocalNodeID: f.nodeA, LocalCredentialSHA256: device.HashCredential("local-credential"), LocalMaxActive: 100, LocalMaxRetained: 100, Suspension: policy}})
	if err != nil {
		t.Fatal(err)
	}
	spec := store.SandboxDeploymentTestSpec("microsandbox")
	raw, _ := json.Marshal(spec)
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_deployment SET specification=$1", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_nodes SET specification_digest=$1,deployment_generation=1", spec.Digest("microsandbox")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.stop)
	f.epoch, err = s.RuntimeOwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.enroll(f.nodeB)
	f.online(f.nodeA)
	f.online(f.nodeB)
	return f
}
func (f *nodeIsolationFixture) enroll(id string) {
	f.t.Helper()
	token, err := store.EnrollmentTestToken(f.store.CreateRuntimeEnrollment(f.t.Context(), store.RuntimeNodeCapacity{MaxActive: 100, MaxRetained: 100}))
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = f.store.EnrollRuntimeNode(f.t.Context(), token, store.RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: store.SandboxDeploymentTestSpec("microsandbox").Digest("microsandbox"), NodeID: id, Credential: strings.Repeat("x", 64), Name: id, Provider: "microsandbox", BackendFingerprint: strings.Repeat("b", 64)})
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *nodeIsolationFixture) online(id string) {
	f.t.Helper()
	connection := uuid.NewString()
	if err := f.store.ConnectRuntimeNode(f.t.Context(), id, connection, f.epoch); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.HeartbeatRuntimeNode(f.t.Context(), id, connection, f.epoch, store.RuntimeNodeHealth{ProviderReady: true}); err != nil {
		f.t.Fatal(err)
	}
}
func (f *nodeIsolationFixture) session(node string, initialize bool) (string, store.Session, store.Environment) {
	f.t.Helper()
	tenant := uuid.NewString()
	input := store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage("{\"agent\":{\"model\":\"test\"},\"environment\":{\"type\":\"openai_hosted\"}}")}
	if initialize {
		input.InitialFiles = []store.InitialFile{{Type: "inline", Path: "/workspace/seed", Data: []byte("retained")}}
	}
	// Placement is automatic: only node stays provider-ready while it is created.
	var others []string
	if err := f.pool.QueryRow(f.t.Context(), "WITH changed AS (UPDATE runtime_nodes SET provider_ready=false WHERE provider_ready AND id<>$1 RETURNING id) SELECT coalesce(array_agg(id::text),'{}') FROM changed", node).Scan(&others); err != nil {
		f.t.Fatal(err)
	}
	session, err := f.store.CreateSession(f.t.Context(), tenant, input)
	if _, restoreErr := f.pool.Exec(f.t.Context(), "UPDATE runtime_nodes SET provider_ready=true WHERE id::text=ANY($1)", others); restoreErr != nil {
		f.t.Fatal(restoreErr)
	}
	if err != nil {
		f.t.Fatal(err)
	}
	env, err := f.store.GetSessionEnvironment(f.t.Context(), tenant, session.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return tenant, session, env
}
func (f *nodeIsolationFixture) provision(tenant string, env store.Environment) store.RuntimeAllocation {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.t.Context(), 3*time.Second)
	defer cancel()
	owner, err := f.worker.ProvisionEnvironment(ctx, tenant, env.ID, f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return owner
}
func (f *nodeIsolationFixture) phase(tenant, environment, phase string) store.RuntimeAllocation {
	f.t.Helper()
	for range 10 {
		if err := f.worker.ReconcileManagedRuntimes(f.t.Context()); err != nil {
			f.t.Fatal(err)
		}
		owner, err := f.store.GetRuntimeAllocation(f.t.Context(), tenant, environment)
		if err != nil {
			f.t.Fatal(err)
		}
		if owner.ComputePhase == phase {
			return owner
		}
	}
	f.t.Fatal("manual lifecycle did not reach " + phase)
	return store.RuntimeAllocation{}
}
func (f *nodeIsolationFixture) run() {
	f.t.Helper()
	ctx, cancel := context.WithCancel(f.t.Context())
	f.runCancel, f.runDone = cancel, make(chan error, 1)
	go func() { f.runDone <- f.worker.Run(ctx) }()
}
func (f *nodeIsolationFixture) stop() {
	f.stopOnce.Do(func() {
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
