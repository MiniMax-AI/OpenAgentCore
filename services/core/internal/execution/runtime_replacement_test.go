package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type retentionProvider struct {
	sandbox.SandboxProvider
	killError                 error
	creates, kills, snapshots int
	bootstraps                chan sandbox.Bootstrap
}

func (p *retentionProvider) ProviderOperations() providercontract.Operations {
	return microsandbox.Operations()
}
func (p *retentionProvider) Create(_ context.Context, q sandbox.Bootstrap) (sandbox.Info, error) {
	p.creates++
	if p.bootstraps != nil {
		p.bootstraps <- q
	}
	return sandbox.Info{Reference: q.Reference, ProviderID: q.AllocationID, State: "running", BootstrapComplete: true, CreateSettled: true}, nil
}
func (p *retentionProvider) KillCompute(context.Context, sandbox.Reference, sandbox.Compute) error {
	p.kills++
	return p.killError
}
func (p *retentionProvider) DeleteSnapshot(context.Context, sandbox.Reference, sandbox.SnapshotIdentity) error {
	p.snapshots++
	return nil
}

func TestExpiredRetainedComputePreservesSessionAndPendingInput(t *testing.T) {
	provider := &retentionProvider{killError: sandbox.ErrComputeUnconfirmed}
	fixture := newWorkspaceSettlementFixture(t, provider)
	r, session := fixture.lifecycle, fixture.session
	owner, err := r.provision(t.Context(), session.TenantID, session.Environment.ID, r.config.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := fixture.storage.Get(t.Context(), session.TenantID, session.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Model the committed completion and suspension receipts. Provider callbacks
	// remain deterministic fixtures; these records do not claim native evidence.
	if _, err = fixture.pool.Exec(t.Context(), `UPDATE environments SET initialization='complete',status='disconnected' WHERE id=$1`, owner.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.pool.Exec(t.Context(), `UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{"retained_native_history":true}}]' WHERE id=$1`, owner.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.pool.Exec(t.Context(), `UPDATE session_devices SET native_session_id='retained-native-session' WHERE device_id=$1`, owner.DeviceID); err != nil {
		t.Fatal(err)
	}
	compute := runtimeCompute{Current: sandbox.Compute{ID: "old-compute"}, Snapshot: &sandbox.SnapshotIdentity{ID: "old-snapshot"}}
	raw, _ := json.Marshal(compute)
	if _, err = fixture.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='suspended',compute_state=$2,compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID, raw); err != nil {
		t.Fatal(err)
	}
	input, err := fixture.sessions.ReserveEnvironmentInput(t.Context(), session.TenantID, session.ID, "after-retention", []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"resume"}]}]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, settled := range []bool{false, true} {
		if settled {
			provider.killError = nil
		}
		owner, err = r.reader.EnvironmentAllocation(t.Context(), owner.Key())
		if err != nil {
			t.Fatal(err)
		}
		err = r.observe(t.Context(), owner)
		if !settled && !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
			t.Fatal("unknown old writer was settled", err)
		}
		if settled && err != nil {
			t.Fatal(err)
		}
		current, err := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
		if err != nil {
			t.Fatal(err)
		}
		want := "cleanup_pending"
		if settled {
			want = "released"
		}
		if current.State != want || current.ID != owner.ID || !current.CreateSettled {
			t.Fatal("compute ownership changed without settlement", current)
		}
		environment, err := r.sessions.GetEnvironment(t.Context(), session.TenantID, owner.EnvironmentID)
		if err != nil || environment.Status != "disconnected" || environment.Initialization != "complete" {
			t.Fatal("compute expiry terminated retained Environment", environment, err)
		}
		pending, err := r.sessions.GetEnvironmentInputReservation(t.Context(), session.TenantID, session.ID, input.ID)
		if err != nil || pending.State != sessions.EnvironmentInputPending {
			t.Fatal("compute expiry consumed pending input", pending, err)
		}
		var nativeID string
		if err = fixture.pool.QueryRow(t.Context(), `SELECT native_session_id FROM session_devices WHERE session_id=$1`, session.ID).Scan(&nativeID); err != nil || nativeID != "retained-native-session" {
			t.Fatal("compute expiry discarded native identity", nativeID, err)
		}
		if _, err = r.workspaces.DeleteBatch(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		retained, err := fixture.storage.Get(t.Context(), session.TenantID, owner.EnvironmentID)
		if err != nil || retained.Reference != binding.Reference || retained.Configuration.ID != binding.Configuration.ID || fixture.control.deletes != 0 {
			t.Fatal("compute expiry changed retained filesystem", retained, err)
		}
	}
	if provider.creates != 1 || provider.snapshots != 1 {
		t.Fatal("expiry retried creation or deleted an unconfirmed writer snapshot", provider.creates, provider.snapshots)
	}
}

func TestRetainedEnvironmentDemandRecreatesCompute(t *testing.T) {
	for _, demand := range []string{"input", "live_file"} {
		t.Run(demand, func(t *testing.T) {
			provider := &retentionProvider{}
			fixture := workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, true))
			r, session := fixture.lifecycle, fixture.session
			owner, err := r.provision(t.Context(), session.TenantID, session.Environment.ID, r.config.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := fixture.storage.Get(t.Context(), session.TenantID, owner.EnvironmentID)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range []struct {
				q   string
				arg any
			}{
				{`UPDATE environments SET initialization='complete',status='disconnected' WHERE id=$1`, owner.EnvironmentID},
				{`UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{"retained_native_history":true}}]' WHERE id=$1`, owner.DeviceID},
				{`UPDATE session_devices SET native_session_id='retained-native-session' WHERE device_id=$1`, owner.DeviceID},
				{`UPDATE runtime_allocations SET compute_phase='suspended',compute_state='{"current":{"id":"old-compute"},"snapshot":{"id":"old-snapshot"}}',compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID},
			} {
				if _, err = fixture.pool.Exec(t.Context(), statement.q, statement.arg); err != nil {
					t.Fatal(err)
				}
			}
			owner, err = r.reader.EnvironmentAllocation(t.Context(), owner.Key())
			if err != nil {
				t.Fatal(err)
			}
			if err = r.observe(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			owner, err = r.reader.EnvironmentAllocation(t.Context(), owner.Key())
			if err != nil || owner.State != "released" {
				t.Fatal(owner, err)
			}
			r.config.Mode = string(sandbox.DeploymentNodes)
			manager := &runtimeManager{ctx: t.Context(), config: r.config, setupGate: make(chan struct{}, 1), nodes: make(map[string]*runtimeNode), workspaces: r.workspaces, sessions: r.sessions, sessionExecution: r.sessionExecution, deployment: r.deployment, deploymentService: r.deployments, deploymentReader: r.reader, lease: r.lease, registry: r.registry}
			// Neither retained storage nor an old receipt is demand by itself.
			if err = manager.reserveReplacementPlacements(t.Context()); err != nil {
				t.Fatal(err)
			}
			candidates, err := r.reader.UnallocatedEnvironments(t.Context(), r.nodeID, "")
			if err != nil || len(candidates) != 0 {
				t.Fatal("idle retained Environment reserved compute", candidates, err)
			}
			if demand == "input" {
				_, err = fixture.sessions.ReserveEnvironmentInput(t.Context(), session.TenantID, session.ID, "replacement-input", []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"resume"}]}]}`)}})
				if err != nil {
					t.Fatal(err)
				}
				if err = manager.reserveReplacementPlacements(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err = r.provisionPending(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				worker := &Worker{runtimes: manager, dispatcher: &Dispatcher{Registry: r.registry, DeploymentReader: r.reader, Deployment: r.deployments, SessionsReader: r.sessions}, stopped: make(chan struct{}), directoryReads: make(chan directoryReadRequest)}
				provider.bootstraps = make(chan sandbox.Bootstrap, 1)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				completed := make(chan error, 1)
				go func() { _, err := worker.ReadEnvironmentDirectory(ctx, *session.Environment, ""); completed <- err }()
				var bootstrap sandbox.Bootstrap
				select {
				case bootstrap = <-provider.bootstraps:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				assertWaiting := func() {
					t.Helper()
					select {
					case err := <-completed:
						t.Fatal("file call returned before Runtime readiness", err)
					case <-worker.directoryReads:
						t.Fatal("file work queued before Runtime readiness")
					case <-time.After(300 * time.Millisecond):
					}
				}
				assertWaiting()
				handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(r.sessions), Registry: r.registry})
				server := httptest.NewServer(http.HandlerFunc(handler.WS))
				defer server.Close()
				query := url.Values{"device_id": {bootstrap.DeviceID}, "version": {proto.Version}}
				conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?"+query.Encode(), http.Header{"Authorization": {"Bearer " + bootstrap.Credential}})
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				assertWaiting()
				heartbeat, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported, Preparation: proto.CapabilitySupported, WorkspaceReadPreparation: proto.CapabilitySupported, RetainedNativeHistory: proto.CapabilitySupported})}}})
				if err = conn.WriteJSON(heartbeat); err != nil {
					t.Fatal(err)
				}
				// The public file entry point may enqueue only after the actual
				// credential-authorized connection has advertised its Harness.
				select {
				case request := <-worker.directoryReads:
					request.reply(directoryReadResult{directory: proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{}}})
				case err := <-completed:
					t.Fatal("file call failed", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if err = <-completed; err != nil {
					t.Fatal(err)
				}
			}
			replacement, err := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
			if err != nil || replacement.ID == owner.ID || replacement.DeviceID == owner.DeviceID || replacement.State != "running" || !replacement.CreateSettled {
				t.Fatal("demand failed to create fresh owner", replacement, err)
			}
			environment, err := r.sessions.GetEnvironment(t.Context(), session.TenantID, owner.EnvironmentID)
			if err != nil || environment.Initialization != "complete" {
				t.Fatal("replacement replayed initialization", environment, err)
			}
			retained, err := fixture.storage.Get(t.Context(), session.TenantID, owner.EnvironmentID)
			if err != nil || retained.Reference != binding.Reference || retained.Configuration.ID != binding.Configuration.ID || provider.creates != 2 {
				t.Fatal("replacement changed immutable filesystem or create count", retained, provider.creates, err)
			}
			var nativeID string
			if err = fixture.pool.QueryRow(t.Context(), `SELECT native_session_id FROM session_devices WHERE session_id=$1`, session.ID).Scan(&nativeID); err != nil || nativeID != "retained-native-session" {
				t.Fatal("replacement lost native history identity", nativeID, err)
			}
		})
	}
}

func workspaceNodeSetup(t *testing.T, external bool) func(Owner, *deployment.Service, deployment.Reader) (string, string) {
	return func(owner Owner, service *deployment.Service, reader deployment.Reader) (string, string) {
		installation := uuid.NewString()
		if err := owner.Deployment.Claim(t.Context(), installation); err != nil {
			t.Fatal(err)
		}
		specification := sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 8192}, Workspace: &workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}, Runtime: &sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}}
		if !external {
			specification.Workspace = nil
			specification.Resources.EnvironmentDiskMiB = 8192
		}
		view, err := owner.Deployment.Initialize(t.Context(), installation, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: specification})
		if err != nil {
			t.Fatal(err)
		}
		token, err := service.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 2})
		if err != nil {
			t.Fatal(err)
		}
		node := deployment.Enrollment{NodeID: uuid.NewString(), Name: "replacement node", Credential: strings.Repeat("n", 64), Provider: view.Provider, BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: fixturePublicURL}
		if _, err = service.Enroll(t.Context(), token.Token, node); err != nil {
			t.Fatal(err)
		}
		epoch, err := reader.OwnerEpoch(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		connection := uuid.NewString()
		if err = service.ConnectNode(t.Context(), node.NodeID, connection, epoch); err != nil {
			t.Fatal(err)
		}
		if err = service.Heartbeat(t.Context(), node.NodeID, connection, epoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
			t.Fatal(err)
		}
		return installation, node.NodeID

	}
}

func TestProvisionUsesReservedGenerationStorageDuringUpdate(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned_ready_external_target", true: "external_ready_owned_target"}[external], func(t *testing.T) {
			provider := &retentionProvider{bootstraps: make(chan sandbox.Bootstrap, 1)}
			f := workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, external))
			r, s := f.lifecycle, f.session
			setup, err := r.deployments.Setup(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			target := setup.Specification
			target.Workspace = &workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
			target.Resources.EnvironmentDiskMiB = 0
			if external {
				target.Workspace = nil
				target.Resources.EnvironmentDiskMiB = 8192
			}
			if _, err = r.deployment.Update(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()}), r.config.InstallationID, sandbox.Selection{Provider: setup.Provider, ExpectedGeneration: setup.Generation, DeploymentSpec: target}); err != nil {
				t.Fatal(err)
			}
			// Neither today's target nor the lane's cached storage mode selects storage.
			r.config.Workspace = target.Workspace
			owner, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			if owner.DeploymentGeneration != setup.Generation {
				t.Fatal("lost reserved generation", owner)
			}
			bootstrap := <-provider.bootstraps
			if (bootstrap.Workspace != nil) != external {
				t.Fatal("storage mode followed target instead of reservation", external, bootstrap.Workspace)
			}
			environment, err := r.sessions.GetEnvironment(t.Context(), s.TenantID, s.Environment.ID)
			if err != nil || environment.ExternalWorkspace != external {
				t.Fatal("immutable binding projection", environment, err)
			}
		})
	}
}

func TestProvisionRejectsRetainedBindingOnOwnedGeneration(t *testing.T) {
	provider := &retentionProvider{bootstraps: make(chan sandbox.Bootstrap, 1)}
	f := workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, false))
	r, s := f.lifecycle, f.session
	if _, err := r.workspaces.Ensure(t.Context(), s.TenantID, s.Environment.ID, r.config.WorkspaceRequirements, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal("binding silently dropped", err)
	}
	if provider.creates != 0 {
		t.Fatal("incompatible compute was created")
	}
	if _, err := r.reader.EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID}); !errors.Is(err, deployment.ErrNotFound) {
		t.Fatal("incompatible compute reserved", err)
	}
}

// A deterministic provider stops after capturing Resume, before subsequent
// wake operations. The allocation, generation and filesystem binding are real.
type resumeStorageProvider struct {
	retentionProvider
	request *sandbox.ResumeRequest
	stop    error
}

func (p *resumeStorageProvider) Resume(_ context.Context, request sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	p.request = &request
	return sandbox.ComputeState{}, p.stop
}

func TestRestoreUsesAllocationGenerationStorageInsteadOfCachedLane(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned_allocation_external_lane", true: "external_allocation_owned_lane"}[external], func(t *testing.T) {
			stop := errors.New("resume request captured")
			provider := &resumeStorageProvider{stop: stop}
			f := workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, external))
			r, s := f.lifecycle, f.session
			owner, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			setup, err := r.deployments.Setup(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			target := setup.Specification
			target.Workspace = &workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
			target.Resources.EnvironmentDiskMiB = 0
			if external {
				target.Workspace = nil
				target.Resources.EnvironmentDiskMiB = 8192
			}
			audit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
			if _, err = r.deployment.Update(audit, r.config.InstallationID, sandbox.Selection{Provider: setup.Provider, ExpectedGeneration: setup.Generation, DeploymentSpec: target}); err != nil {
				t.Fatal(err)
			}
			r.config.Workspace = target.Workspace
			r.config.Resources = target.Resources
			state := runtimeCompute{Target: &sandbox.Compute{ID: "replacement-compute", Generation: 2}, Snapshot: &sandbox.SnapshotIdentity{ID: "snapshot"}, RestoreID: uuid.NewString()}
			if err = r.restoreCompute(t.Context(), provider, owner, state, false); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			if provider.request == nil || (provider.request.Workspace != nil) != external {
				t.Fatal("Resume binding followed cached lane", external, provider.request)
			}
			if external {
				stored, err := f.storage.Get(t.Context(), s.TenantID, s.Environment.ID)
				if err != nil || provider.request.Workspace.Attachment.Reference != stored.Reference {
					t.Fatal("Resume lost retained object identity", err)
				}
			}
			provider.request = nil
			stale := owner
			stale.ID = uuid.NewString()
			if err = r.restoreCompute(t.Context(), provider, stale, state, false); !errors.Is(err, sandbox.ErrOwnership) || provider.request != nil {
				t.Fatal("stale owner reached native Resume", err)
			}
		})
	}
}
