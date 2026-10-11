package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/gorilla/websocket"
)

// lostComputeProvider models the owning node's exact-incarnation observation
// and cleanup. These callbacks are deterministic fixtures, not native evidence.
type lostComputeProvider struct {
	retentionProvider
	observe func(sandbox.Compute) (sandbox.ComputeState, error)
	onKill  func()
	killed  []sandbox.Compute
}

func (p *lostComputeProvider) GetCompute(_ context.Context, _ sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.observe(c)
}
func (p *lostComputeProvider) KillCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) error {
	p.killed = append(p.killed, c)
	if p.onKill != nil {
		p.onKill()
	}
	return p.retentionProvider.KillCompute(ctx, r, c)
}

var lostCompute = sandbox.Compute{Name: "old-compute", ID: "old-compute-id"}

func observedStatus(status string) func(sandbox.Compute) (sandbox.ComputeState, error) {
	return func(c sandbox.Compute) (sandbox.ComputeState, error) {
		return sandbox.ComputeState{Compute: c, Status: status, BootstrapComplete: true}, nil
	}
}

func observedError(err error) func(sandbox.Compute) (sandbox.ComputeState, error) {
	return func(sandbox.Compute) (sandbox.ComputeState, error) { return sandbox.ComputeState{}, err }
}

// lostObservations are the owning node's confirmations of loss: typed
// absence, or a verified terminal status of the exact incarnation.
var lostObservations = map[string]func(sandbox.Compute) (sandbox.ComputeState, error){
	"absent":  observedError(sandbox.ErrNotFound),
	"stopped": observedStatus("stopped"),
	"crashed": observedStatus("crashed"),
}

type lostComputeFixture struct {
	workspaceSettlementFixture
	provider  *lostComputeProvider
	owner     deployment.Allocation
	bootstrap sandbox.Bootstrap
	input     sessions.EnvironmentInputReservation
}

// newLostComputeFixture commits a settled running incarnation of a Session
// whose completed initialization and native identity are retained.
func newLostComputeFixture(t *testing.T, external bool, seed ...string) lostComputeFixture {
	t.Helper()
	provider := &lostComputeProvider{retentionProvider: retentionProvider{bootstraps: make(chan sandbox.Bootstrap, 2)}, observe: func(sandbox.Compute) (sandbox.ComputeState, error) {
		return sandbox.ComputeState{}, sandbox.ErrNotFound
	}}
	f := lostComputeFixture{workspaceSettlementFixture: workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, external)), provider: provider}
	r, session := f.lifecycle, f.session
	owner, err := r.provision(t.Context(), session.TenantID, session.Environment.ID, r.config.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	f.bootstrap = <-provider.bootstraps
	state, _ := json.Marshal(runtimeCompute{Current: lostCompute})
	bind := strings.NewReplacer("$1", "'"+owner.EnvironmentID+"'", "$2", "'"+owner.DeviceID+"'", "$3", "'"+owner.ID+"'", "$4", "'"+string(state)+"'")
	exec := func(statements []string) {
		for _, statement := range statements {
			if _, err := f.pool.Exec(t.Context(), bind.Replace(statement)); err != nil {
				t.Fatal(statement, err)
			}
		}
	}
	exec([]string{
		`UPDATE environments SET initialization='complete',status='disconnected' WHERE id=$1`,
		`UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{"retained_native_history":true}}]' WHERE id=$2`,
		`UPDATE session_devices SET native_session_id='retained-native-session' WHERE device_id=$2`,
		`UPDATE runtime_allocations SET compute_phase='running',compute_state=$4,compute_revision=7 WHERE id=$3`,
	})
	if f.input, err = f.sessions.ReserveEnvironmentInput(t.Context(), session.TenantID, session.ID, "after-loss", []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"continue"}]}]}`)}}); err != nil {
		t.Fatal(err)
	}
	exec(seed)
	r.connections = map[string]*runtimeConnection{}
	if f.owner, err = r.reader.EnvironmentAllocation(t.Context(), owner.Key()); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f lostComputeFixture) current(t *testing.T) deployment.Allocation {
	t.Helper()
	current, err := f.lifecycle.reader.EnvironmentAllocation(t.Context(), f.owner.Key())
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func (f lostComputeFixture) revoked(t *testing.T, device string) bool {
	t.Helper()
	var revoked bool
	if err := f.pool.QueryRow(t.Context(), `SELECT revoked_at IS NOT NULL FROM devices WHERE id=$1`, device).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

// assertRetained checks that the Session, its pending input, native identity
// and filesystem survived, whatever happened to compute ownership.
func (f lostComputeFixture) assertRetained(t *testing.T, status string, initialization ...string) {
	t.Helper()
	r, session := f.lifecycle, f.session
	initialization = append(initialization, "complete")
	environment, err := r.sessions.GetEnvironment(t.Context(), session.TenantID, session.Environment.ID)
	if err != nil || environment.Status != status || environment.Initialization != initialization[0] {
		t.Fatal("Environment changed", environment.Status, environment.Initialization, err)
	}
	pending, err := r.sessions.GetEnvironmentInputReservation(t.Context(), session.TenantID, session.ID, f.input.ID)
	if err != nil || pending.State != sessions.EnvironmentInputPending || !pending.Deadline.Equal(f.input.Deadline) {
		t.Fatal("pending input changed", pending, err)
	}
	var nativeID string
	if err = f.pool.QueryRow(t.Context(), `SELECT native_session_id FROM session_devices WHERE session_id=$1`, session.ID).Scan(&nativeID); err != nil || nativeID != "retained-native-session" {
		t.Fatal("native identity changed", nativeID, err)
	}
	if _, err = r.workspaces.DeleteBatch(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if f.control.deletes != 0 {
		t.Fatal("filesystem deleted", f.control.deletes)
	}
}

// demand runs the common placement scan and pending provisioning for
// retained input, as TestRetainedEnvironmentDemandRecreatesCompute does.
func (f lostComputeFixture) demand(t *testing.T) {
	t.Helper()
	r := f.lifecycle
	r.config.Mode = string(sandbox.DeploymentNodes)
	manager := &runtimeManager{ctx: t.Context(), config: r.config, setupGate: make(chan struct{}, 1), inventory: make(chan struct{}, 1), nodes: make(map[string]*runtimeNode), workspaces: r.workspaces, sessions: r.sessions, sessionExecution: r.sessionExecution, deployment: r.deployment, deploymentService: r.deployments, deploymentReader: r.reader, lease: r.lease, registry: r.registry}
	if err := manager.reservePlacements(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.provisionPending(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmedLostRunningComputeContinuesRetainedSession(t *testing.T) {
	for name, observe := range lostObservations {
		t.Run(name, func(t *testing.T) {
			f := newLostComputeFixture(t, true)
			f.provider.observe = observe
			r := f.lifecycle
			binding, err := f.storage.Get(t.Context(), f.session.TenantID, f.owner.EnvironmentID)
			if err != nil {
				t.Fatal(err)
			}
			f.provider.killError = sandbox.ErrComputeUnconfirmed
			if err = r.observe(t.Context(), f.owner); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
				t.Fatal("unconfirmed cleanup settled lost compute", err)
			}
			// Retained input cannot reserve another writer before confirmed release.
			f.demand(t)
			if current := f.current(t); current.ID != f.owner.ID || current.State != "running" || current.ComputePhase != "running" || f.provider.creates != 1 || f.revoked(t, f.owner.DeviceID) {
				t.Fatal("replacement admitted before confirmed release", current, f.provider.creates)
			}
			f.provider.killError = nil
			if err = r.observe(t.Context(), f.current(t)); err != nil {
				t.Fatal(err)
			}
			released := f.current(t)
			if released.ID != f.owner.ID || released.State != "released" || !released.CreateSettled || !f.revoked(t, f.owner.DeviceID) {
				t.Fatal("confirmed loss did not release ownership", released)
			}
			if !slices.Equal(f.provider.killed, []sandbox.Compute{lostCompute, lostCompute}) || f.provider.snapshots != 0 {
				t.Fatal("cleanup did not target the exact incarnation", f.provider.killed, f.provider.snapshots)
			}
			f.assertRetained(t, "disconnected")

			f.demand(t)
			replacement := f.current(t)
			if replacement.ID == f.owner.ID || replacement.DeviceID == f.owner.DeviceID || replacement.State != "running" || !replacement.CreateSettled || f.provider.creates != 2 {
				t.Fatal("retained input did not continue on new compute", replacement, f.provider.creates)
			}
			next := <-f.provider.bootstraps
			if next.Workspace == nil || next.Workspace.Attachment.Reference != binding.Reference || next.DeviceID != replacement.DeviceID {
				t.Fatal("replacement did not attach the retained filesystem", next.Workspace)
			}
			f.assertRetained(t, "disconnected")
		})
	}
}

func TestUnconfirmedLostComputeKeepsOwnership(t *testing.T) {
	type negative struct {
		name     string
		owned    bool
		seed     []string
		observe  func(sandbox.Compute) (sandbox.ComputeState, error)
		killErr  error
		livePeer bool
		want     error
		kills    int
		status   string
		initial  string
	}
	foreign := func(status string) func(sandbox.Compute) (sandbox.ComputeState, error) {
		return func(c sandbox.Compute) (sandbox.ComputeState, error) {
			c.ID = "foreign-compute-id"
			return sandbox.ComputeState{Compute: c, Status: status}, nil
		}
	}
	tests := []negative{
		{name: "paused", observe: observedStatus("paused"), want: sandbox.ErrComputeUnconfirmed},
		{name: "created", observe: observedStatus("created"), want: sandbox.ErrComputeUnconfirmed},
		{name: "starting", observe: observedStatus("starting"), want: sandbox.ErrComputeUnconfirmed},
		{name: "draining", observe: observedStatus("draining"), want: sandbox.ErrComputeUnconfirmed},
		{name: "unknown_status", observe: observedStatus("unknown"), want: sandbox.ErrComputeUnconfirmed},
		{name: "running_without_peer", observe: observedStatus("running"), want: runtimegateway.ErrDeviceNotRegistered},
		{name: "transport_unconfirmed", observe: observedError(sandbox.ErrComputeUnconfirmed), want: sandbox.ErrComputeUnconfirmed},
		{name: "node_unavailable", observe: observedError(placement.ErrNodeUnavailable), want: placement.ErrNodeUnavailable},
		{name: "timeout", observe: observedError(context.DeadlineExceeded), want: context.DeadlineExceeded},
		{name: "identity_mismatch", observe: observedError(sandbox.ErrOwnership), want: sandbox.ErrOwnership},
		{name: "crashed_foreign_identity", observe: foreign("crashed"), want: sandbox.ErrOwnership},
		{name: "stopped_foreign_identity", observe: foreign("stopped"), want: sandbox.ErrOwnership},
	}
	// Each guard holds for typed absence and for a verified terminal status;
	// a terminal status that cannot be released stays unconfirmed.
	for _, guard := range []negative{
		{name: "unknown_creation", seed: []string{`UPDATE runtime_allocations SET create_settled=false WHERE id=$3`}, want: sandbox.ErrNotFound},
		{name: "no_compute_id", seed: []string{`UPDATE runtime_allocations SET compute_state='{"current":{"Name":"old-compute"}}' WHERE id=$3`}, want: sandbox.ErrOwnership},
		{name: "restore_intent", seed: []string{`UPDATE runtime_allocations SET compute_state='{"current":{"Name":"old-compute","ID":"old-compute-id"},"target":{"Name":"next","Generation":1},"restore_id":"attempt"}' WHERE id=$3`}, want: sandbox.ErrNotFound},
		{name: "owned_workspace", owned: true, want: sandbox.ErrNotFound},
		{name: "initialization_incomplete", seed: []string{`UPDATE environments SET initialization='running' WHERE id=$1`}, want: sandbox.ErrNotFound, initial: "running"},
		{name: "terminal_environment", seed: []string{`UPDATE environments SET status='failed' WHERE id=$1`}, want: sandbox.ErrNotFound, status: "failed"},
		{name: "native_history_undeclared", seed: []string{`UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{}}]' WHERE id=$2`}, want: sandbox.ErrNotFound},
		{name: "live_peer", livePeer: true, want: sandbox.ErrComputeUnconfirmed},
		{name: "cleanup_failure", killErr: sandbox.ErrComputeUnconfirmed, want: sandbox.ErrComputeUnconfirmed, kills: 1},
	} {
		tests = append(tests, guard)
		for _, status := range []string{"stopped", "crashed"} {
			terminal := guard
			terminal.name, terminal.observe = status+"_"+guard.name, observedStatus(status)
			if errors.Is(guard.want, sandbox.ErrNotFound) {
				terminal.want = sandbox.ErrComputeUnconfirmed
			}
			tests = append(tests, terminal)
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newLostComputeFixture(t, !test.owned, test.seed...)
			r := f.lifecycle
			if test.observe != nil {
				f.provider.observe = test.observe
			}
			f.provider.killError = test.killErr
			if test.livePeer {
				handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(r.sessions), Registry: r.registry})
				server := httptest.NewServer(http.HandlerFunc(handler.WS))
				defer server.Close()
				query := url.Values{"device_id": {f.bootstrap.DeviceID}, "version": {proto.Version}}
				conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?"+query.Encode(), http.Header{"Authorization": {"Bearer " + f.bootstrap.Credential}})
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := authorizedRuntimePeer(t.Context(), r.sessions, r.registry, f.owner.DeviceID); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("peer did not register")
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			before := f.current(t)
			for range 2 {
				if err := r.observe(t.Context(), f.current(t)); !errors.Is(err, test.want) {
					t.Fatalf("observe = %v, want %v", err, test.want)
				}
			}
			current := f.current(t)
			if current.ID != before.ID || current.State != before.State || current.ComputePhase != "running" || current.ComputeRevision != before.ComputeRevision || current.CreateSettled != before.CreateSettled || string(current.ComputeState) != string(before.ComputeState) {
				t.Fatal("ownership changed without confirmed loss", current)
			}
			if f.revoked(t, f.owner.DeviceID) || len(f.provider.killed) != 2*test.kills || f.provider.creates != 1 {
				t.Fatal("unconfirmed loss revoked or cleaned compute", f.provider.killed, f.provider.creates)
			}
			status := test.status
			if status == "" {
				status = "disconnected"
			}
			if test.livePeer {
				status = "connected"
			}
			if test.initial != "" {
				f.assertRetained(t, status, test.initial)
				return
			}
			f.assertRetained(t, status)
		})
	}
}

func TestLostComputeReleaseSerializesWithConcurrentIntent(t *testing.T) {
	for name, observe := range lostObservations {
		t.Run(name, func(t *testing.T) {
			t.Run("restart_after_kill", func(t *testing.T) {
				f := newLostComputeFixture(t, true)
				f.provider.observe = observe
				r := f.lifecycle
				// The first owner confirms cleanup, then stops before the release commit.
				stopped, stop := context.WithCancel(t.Context())
				f.provider.onKill = stop
				if err := r.observe(stopped, f.owner); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if current := f.current(t); current.State != "running" || f.revoked(t, f.owner.DeviceID) {
					t.Fatal("interrupted owner committed release", current)
				}
				f.provider.onKill = nil
				restarted := &runtimeLifecycle{nodeID: r.nodeID, registry: runtimegateway.NewRegistry(), sessions: r.sessions, sessionExecution: r.sessionExecution, deployment: r.deployment, deployments: r.deployments, reader: r.reader, lease: r.lease, workspaces: r.workspaces, config: r.config, connections: map[string]*runtimeConnection{}}
				if err := restarted.observe(t.Context(), f.current(t)); err != nil {
					t.Fatal(err)
				}
				released := f.current(t)
				if released.State != "released" || !slices.Equal(f.provider.killed, []sandbox.Compute{lostCompute, lostCompute}) {
					t.Fatal("restart did not converge on the same incarnation", released, f.provider.killed)
				}
				// A duplicate observation of the stale owner cannot change the receipt.
				if err := r.observeCompute(t.Context(), f.owner); !errors.Is(err, deployment.ErrAllocationConflict) {
					t.Fatal("stale duplicate release", err)
				}
				if current := f.current(t); current.State != "released" || current.ComputeRevision != released.ComputeRevision || len(f.provider.killed) != 2 || f.provider.creates != 1 {
					t.Fatal("duplicate changed the released receipt", current, f.provider.killed)
				}
				f.assertRetained(t, "disconnected")
			})
			for _, test := range []struct {
				name      string
				statement string
				want      error
				status    string
			}{
				{name: "compute_revision", statement: `UPDATE runtime_allocations SET compute_revision=compute_revision+1 WHERE environment_id=$1`, want: deployment.ErrAllocationConflict, status: "disconnected"},
				{name: "archive", statement: `UPDATE environments SET status='expired' WHERE id=$1`, want: deployment.ErrAllocationConflict, status: "expired"},
				{name: "deletion", statement: `UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=(SELECT session_id FROM environments WHERE id=$1)`, want: sessions.ErrNotFound},
			} {
				t.Run(test.name, func(t *testing.T) {
					f := newLostComputeFixture(t, true)
					f.provider.observe = observe
					f.provider.onKill = func() {
						if _, err := f.pool.Exec(t.Context(), test.statement, f.owner.EnvironmentID); err != nil {
							t.Error(err)
						}
					}
					if err := f.lifecycle.observe(t.Context(), f.owner); !errors.Is(err, test.want) {
						t.Fatalf("observe = %v, want %v", err, test.want)
					}
					current := f.current(t)
					if current.State != "running" || current.ComputePhase != "running" || f.revoked(t, f.owner.DeviceID) || f.provider.creates != 1 {
						t.Fatal("concurrent intent lost to compute release", current)
					}
					if test.status != "" {
						f.assertRetained(t, test.status)
					} else if _, err := f.lifecycle.workspaces.DeleteBatch(t.Context(), ""); err != nil || f.control.deletes != 0 {
						t.Fatal("lost release deleted the filesystem", err)
					}
				})
			}
		})
	}
}
