package execution

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"strings"
	"sync"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
	"github.com/google/uuid"
)

type boundedSettlementControl struct{ *settlementWorkspaceControl }

func (c boundedSettlementControl) Declaration() workspacefs.Declaration {
	d := c.settlementWorkspaceControl.Declaration()
	d.CapacityQuota = true
	return d
}

type boundedSettlementControls struct{ control boundedSettlementControl }

func (c boundedSettlementControls) Control(workspacefs.ConfigurationRecord) (workspacefs.Control, error) {
	return c.control, nil
}
func (c boundedSettlementControls) Normalize(config workspacefs.Configuration, credential json.RawMessage) (workspacefs.ConfigurationRecord, error) {
	return workspacefs.ConfigurationRecord{Configuration: config, Credential: credential}, nil
}

func TestWorkspaceQuotaRefusalYieldsComputeToAnotherTenant(t *testing.T) {
	provider := &retentionProvider{}
	f := newWorkspaceSettlementFixture(t, provider)
	r, a := f.lifecycle, f.session
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_deployment SET specification=jsonb_set(jsonb_set(specification,'{resources,environment_disk_mib}','1024'),'{workspace,capacity_quota}','true')`); err != nil {
		t.Fatal(err)
	}
	writer := f.writer
	r.workspaces = workspaces.NewExecution(f.storage, writer, boundedSettlementControls{boundedSettlementControl{f.control}}, r.lease)
	spec, err := r.workspaceSpecification(t.Context(), deployment.AllocationKey{TenantID: a.TenantID, EnvironmentID: a.Environment.ID}, "")
	if err != nil {
		t.Fatal(err)
	}
	capacity := spec.Resources.EnvironmentDiskMiB
	if capacity == 0 {
		t.Fatal("fixture requires bounded workspace")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE workspace_storage_admission SET tenant_capacity_mib=$1`, capacity); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET max_active=1,max_retained=3 WHERE id=$1`, r.nodeID); err != nil {
		t.Fatal(err)
	}
	// An independent retained object consumes A's tenant storage, without compute.
	storageSession, storageEnvironment := uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration) VALUES($1,$2,'codex',$3,'fixture','{"environment":{"type":"openai_hosted"}}')`, storageSession, a.TenantID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO environments(id,session_id) VALUES($1,$2)`, storageEnvironment, storageSession); err != nil {
		t.Fatal(err)
	}
	occupied, err := writer.Bind(t.Context(), a.TenantID, storageEnvironment, f.control.configuration.ID, capacity)
	if err != nil {
		t.Fatal(err)
	}
	created, err := f.sessions.CreateSession(t.Context(), uuid.NewString(), sessions.CreateSession{SupportsRetainedNativeHistory: true, Creator: identity.Subject{Kind: "service_account", ID: "fixture"}, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`), ModelProvider: &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-key"}, ModelProviderSource: v1.ExecutionSourceSession})
	if err != nil {
		t.Fatal(err)
	}
	b := created.Session
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET status='failed' WHERE id=$1`, storageEnvironment); err != nil {
		t.Fatal(err)
	}
	_, err = r.provision(t.Context(), a.TenantID, a.Environment.ID, r.config.InstallationID)
	if !errors.Is(err, workspaces.ErrCapacityExhausted) {
		t.Fatal("not the real quota refusal", err)
	}
	nodes, err := r.reader.Nodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Active != 0 || provider.creates != 0 {
		t.Fatalf("quota refusal retained compute capacity without a VM: nodes=%+v creates=%d", nodes, provider.creates)
	}
	r.config.Mode = string(sandbox.DeploymentNodes)
	manager := &runtimeManager{ctx: t.Context(), config: r.config, nodes: map[string]*runtimeNode{r.nodeID: {lifecycle: r}}, workspaces: r.workspaces, sessions: r.sessions, sessionExecution: r.sessionExecution, deployment: r.deployment, deploymentService: r.deployments, deploymentReader: r.reader, lease: r.lease, registry: r.registry}
	for range 3 {
		if err := manager.reservePlacements(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := r.provisionPending(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	allocated, err := r.reader.EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: b.TenantID, EnvironmentID: b.Environment.ID})
	if err != nil || allocated.State != "running" || provider.creates != 1 {
		t.Fatal("quota refused A starved admissible B", allocated, err, provider.creates)
	}
	if err := f.sessions.DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: a.TenantID, SessionID: storageSession}); err != nil {
		t.Fatal(err)
	}
	if err := writer.BeginDelete(t.Context(), occupied.Reference); err != nil {
		t.Fatal(err)
	}
	if err := writer.MarkDeleted(t.Context(), occupied.Reference); err != nil {
		t.Fatal(err)
	}
	if err := f.sessions.DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: b.TenantID, SessionID: b.ID}); err != nil {
		t.Fatal(err)
	}
	allocated, err = r.deployment.RequestCleanup(t.Context(), allocated)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.KillCompute(t.Context(), runtimeReference(allocated), sandbox.Compute{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.deployment.ReleaseAllocation(t.Context(), allocated); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := manager.reservePlacements(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := r.provisionPending(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := r.reader.EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: a.TenantID, EnvironmentID: a.Environment.ID})
	if err != nil || recovered.State != "running" || provider.creates != 2 {
		t.Fatal("storage release did not recover A", recovered, err, provider.creates)
	}
}

// An unsettled filesystem create keeps its durable identity while yielding
// compute. In contrast, an unsettled compute create already owns its slot.
type unsettledCapacityControl struct {
	boundedSettlementControl
	refs []workspacefs.Reference
}

func (c *unsettledCapacityControl) Create(ctx context.Context, ref workspacefs.Reference, capacity uint32) (workspacefs.Attachment, error) {
	c.refs = append(c.refs, ref)
	if len(c.refs) == 1 {
		return workspacefs.Attachment{}, workspacefs.ErrUnconfirmed
	}
	return c.settlementWorkspaceControl.Create(ctx, ref, capacity)
}

type unsettledCapacityControls struct {
	boundedSettlementControls
	control *unsettledCapacityControl
}

func (c unsettledCapacityControls) Control(workspacefs.ConfigurationRecord) (workspacefs.Control, error) {
	return c.control, nil
}
func TestUnsettledFilesystemPreparationYieldsOnlyCompute(t *testing.T) {
	provider := &retentionProvider{}
	f := newWorkspaceSettlementFixture(t, provider)
	r, s := f.lifecycle, f.session
	control := &unsettledCapacityControl{boundedSettlementControl: boundedSettlementControl{f.control}}
	r.workspaces = workspaces.NewExecution(f.storage, f.writer, unsettledCapacityControls{control: control}, r.lease)
	key := deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID}
	if _, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID); !errors.Is(err, workspacefs.ErrUnconfirmed) {
		t.Fatal(err)
	}
	record, err := f.storage.Get(t.Context(), s.TenantID, s.Environment.ID)
	if err != nil || record.State != workspaces.Creating || provider.creates != 0 {
		t.Fatal(record, err, provider.creates)
	}
	target, err := r.reader.LifecyclePlacement(t.Context(), key)
	if err != nil || !target.PlacementReleased || target.AllocationID != "" {
		t.Fatal(target, err)
	}
	// A newer generation without external storage must not displace this
	// unallocated Session's already-bound filesystem onto owned ephemeral disk.
	setup, err := r.deployments.Setup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	updated := setup.Specification
	updated.Workspace = nil
	updated.Resources.EnvironmentDiskMiB = 8192
	view, err := r.deployment.Update(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()}), r.config.InstallationID, sandbox.Selection{Provider: setup.Provider, ExpectedGeneration: setup.Generation, DeploymentSpec: updated})
	if err != nil {
		t.Fatal(err)
	}
	token, err := r.deployments.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 2})
	if err != nil {
		t.Fatal(err)
	}
	newer := deployment.Enrollment{NodeID: uuid.NewString(), Name: "newer owned workspace", Credential: strings.Repeat("n", 64), Provider: view.Provider, BackendFingerprint: strings.Repeat("c", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: fixturePublicURL}
	if _, err := r.deployments.Enroll(t.Context(), token.Token, newer); err != nil {
		t.Fatal(err)
	}
	epoch, err := r.reader.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	connection := uuid.NewString()
	if err := r.deployments.ConnectNode(t.Context(), newer.NodeID, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := r.deployments.Heartbeat(t.Context(), newer.NodeID, connection, epoch, deployment.NodeHealth{ProviderReady: true}, []sandbox.GenerationStatus{{Generation: view.Generation, SpecificationDigest: view.SpecificationDigest, State: "ready", Checkpoint: &sandbox.CheckpointCompatibility{ArtifactDomain: "fixture-store", ExecutionClass: "fixture-runtime"}}}); err != nil {
		t.Fatal(err)
	}
	demands, _, err := r.reader.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(demands) != 1 || !demands[0].Retained {
		t.Fatal("bound FS omitted from pressure eligibility", demands, err)
	}
	selected, err := r.deployment.EnsurePlacement(t.Context(), key, r.config.InstallationID)
	if err != nil || selected.NodeID != r.nodeID || selected.Generation != setup.Generation {
		t.Fatal("selected generation cannot attach retained filesystem", selected, err)
	}
	if _, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID); err != nil {
		t.Fatal(err)
	}
	if len(control.refs) != 2 || control.refs[0] != control.refs[1] || control.refs[0] != record.Reference || provider.creates != 1 {
		t.Fatal("retry replaced filesystem identity or duplicated compute", control.refs, provider.creates)
	}
}
func TestPreparationReleaseFencesPlacementAndAllocation(t *testing.T) {
	f := newWorkspaceSettlementFixture(t, &unknownWorkspaceCreation{})
	r, s := f.lifecycle, f.session
	key := deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID}
	target, err := r.reader.LifecyclePlacement(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	expected := placement.Reserved{NodeID: target.PlacementNodeID, Generation: target.PlacementGeneration}
	for _, stale := range []placement.Reserved{{NodeID: uuid.NewString(), Generation: expected.Generation}, {NodeID: expected.NodeID, Generation: expected.Generation + 1}} {
		if err := r.deployment.ReleaseFailedPlacement(t.Context(), key, stale); err != nil {
			t.Fatal(err)
		}
		current, err := r.reader.LifecyclePlacement(t.Context(), key)
		if err != nil || current.PlacementReleased {
			t.Fatal("stale attempt released newer placement", current, err)
		}
	}
	owner, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
	if !errors.Is(err, sandbox.ErrComputeUnconfirmed) || owner.ID == "" {
		t.Fatal(owner, err)
	}
	if err := r.deployment.ReleaseFailedPlacement(t.Context(), key, expected); err != nil {
		t.Fatal(err)
	}
	current, err := r.reader.LifecyclePlacement(t.Context(), key)
	if err != nil || current.PlacementReleased || current.AllocationID != owner.ID {
		t.Fatal("unknown compute lost its slot", current, err)
	}
}

func TestPreparationReleaseSerializesWithAllocationAndTerminalSession(t *testing.T) {
	for _, action := range []string{"allocation", "archive", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newWorkspaceSettlementFixture(t, &retentionProvider{})
			r, s := f.lifecycle, f.session
			key := deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID}
			target, err := r.reader.LifecyclePlacement(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			expected := placement.Reserved{NodeID: target.PlacementNodeID, Generation: target.PlacementGeneration}
			var wg sync.WaitGroup
			var releaseErr, actionErr error
			wg.Go(func() { releaseErr = r.deployment.ReleaseFailedPlacement(t.Context(), key, expected) })
			wg.Go(func() {
				switch action {
				case "allocation":
					_, actionErr = r.deployment.ReserveAllocation(t.Context(), key, r.config.InstallationID, runtimedevice.HashCredential("fixture"))
				case "archive":
					_, actionErr = r.deployment.ArchiveSession(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ProjectID: f.projectID, RequestID: uuid.NewString(), TraceID: uuid.NewString()}), s.TenantID, s.ID, expected.Generation)
				case "delete":
					actionErr = f.sessions.DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: s.TenantID, SessionID: s.ID})
				}
			})
			wg.Wait()
			if releaseErr != nil {
				t.Fatal(releaseErr)
			}
			current, err := r.reader.LifecyclePlacement(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			if action == "allocation" {
				if actionErr == nil && (current.AllocationID == "" || current.PlacementReleased) {
					t.Fatal("allocated compute lost slot", current)
				}
				if actionErr != nil && current.AllocationID != "" {
					t.Fatal("failed allocation persisted", current, actionErr)
				}
			} else {
				if actionErr != nil {
					t.Fatal(actionErr)
				}
				if !current.PlacementReleased {
					t.Fatal("terminal session retained empty slot", current)
				}
				if _, err := r.deployment.EnsurePlacement(t.Context(), key, r.config.InstallationID); err == nil {
					t.Fatal("terminal session reacquired capacity")
				}
			}
		})
	}
}

func TestPreparationRejectsStaleNodeLaneBeforeAllocation(t *testing.T) {
	provider := &retentionProvider{}
	f := newWorkspaceSettlementFixture(t, provider)
	r, s := f.lifecycle, f.session
	r.nodeID = uuid.NewString() // A caller routed before reassignment now holds an obsolete lane.
	_, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
	if !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal(err)
	}
	if _, err := r.reader.EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: s.TenantID, EnvironmentID: s.Environment.ID}); !errors.Is(err, deployment.ErrNotFound) {
		t.Fatal("stale lane reserved undelivered compute", err)
	}
	if provider.creates != 0 {
		t.Fatal(provider.creates)
	}
}

type unavailableCapacityControls struct{ settlementWorkspaceControls }

func (c unavailableCapacityControls) Control(workspacefs.ConfigurationRecord) (workspacefs.Control, error) {
	return nil, workspacefs.ErrUnavailable
}
func TestRetainedPreparationFailureYieldsToWaitingDemand(t *testing.T) {
	f := newWorkspaceSettlementFixture(t, &retentionProvider{})
	r, s := f.lifecycle, f.session
	owner, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	// Seed an already-qualified completion and expired checkpoint; the ordinary
	// lifecycle must settle and release it before cold replacement is eligible.
	for _, q := range []struct {
		sql string
		id  string
	}{
		{`UPDATE environments SET initialization='complete',status='disconnected' WHERE id=$1`, owner.EnvironmentID},
		{`UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{"retained_native_history":true}}]' WHERE id=$1`, owner.DeviceID},
		{`UPDATE runtime_allocations SET compute_phase='suspended',compute_state='{"current":{"id":"old-compute"},"snapshot":{"id":"old-snapshot","compatibility":{"artifact_domain":"fixture-store","execution_class":"fixture-runtime"}}}',compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID},
	} {
		if _, err := f.pool.Exec(t.Context(), q.sql, q.id); err != nil {
			t.Fatal(err)
		}
	}
	owner, err = r.reader.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.observe(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	input, err := f.sessions.ReserveEnvironmentInput(t.Context(), s.TenantID, s.ID, "retry-input", []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"resume"}]}]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.sessions.CreateSession(t.Context(), s.TenantID, sessions.CreateSession{SupportsRetainedNativeHistory: true, Creator: identity.Subject{Kind: "service_account", ID: "fixture"}, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: s.Configuration, ModelProvider: &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-key"}, ModelProviderSource: v1.ExecutionSourceSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.deployment.EnsurePlacement(t.Context(), owner.Key(), r.config.InstallationID); err != nil {
		t.Fatal(err)
	}
	filesystems := r.workspaces
	r.workspaces = workspaces.NewExecution(f.storage, f.writer, unavailableCapacityControls{}, r.lease)
	if _, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID); !errors.Is(err, workspacefs.ErrUnavailable) {
		t.Fatal(err)
	}
	r.workspaces = filesystems
	r.config.Mode = string(sandbox.DeploymentNodes)
	manager := &runtimeManager{ctx: t.Context(), config: r.config, nodes: map[string]*runtimeNode{r.nodeID: {lifecycle: r}}, deployment: r.deployment, deploymentReader: r.reader, lease: r.lease}
	if err := manager.reservePlacements(t.Context()); err != nil {
		t.Fatal(err)
	}
	placed, err := r.reader.LifecyclePlacement(t.Context(), deployment.AllocationKey{TenantID: b.Session.TenantID, EnvironmentID: b.Session.Environment.ID})
	if err != nil || placed.PlacementNodeID != r.nodeID || placed.PlacementReleased {
		t.Fatal("retained retry starved waiting demand", placed, err)
	}
	pending, err := r.sessions.GetEnvironmentInputReservation(t.Context(), s.TenantID, s.ID, input.ID)
	if err != nil || !pending.Deadline.Equal(input.Deadline) || pending.State != sessions.EnvironmentInputPending {
		t.Fatal("yield changed original input deadline", pending, err)
	}
}
