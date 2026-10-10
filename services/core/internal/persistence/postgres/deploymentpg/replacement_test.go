package deploymentpg_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
)

// releaseRetainedFixture records native cleanup completion after exercising the
// production expiry decision. It does not claim to qualify a native provider.
func releaseRetainedFixture(t *testing.T, f fixture, changes *deployment.ExecutionOperations, owner deployment.Allocation) deployment.Allocation {
	t.Helper()
	config := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO workspace_fs_configurations(id,adapter,parameters) VALUES($1,'fixture','{}')`, config); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO environment_workspaces(object_id,environment_id,configuration_id,state,attachment) VALUES($1,$2,$3,'ready','{}') ON CONFLICT(environment_id) DO NOTHING`, uuid.NewString(), owner.EnvironmentID, config); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{"retained_native_history":true}}]' WHERE id=$1`, owner.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET state='running',create_settled=true,compute_phase='suspended',compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	owner, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil {
		t.Fatal(err)
	}
	owner, err = changes.RequestCleanup(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = changes.ReleaseAllocation(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestReplacementKeepsSessionIdentityAndFencesPriorOwner(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	firstNode := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, firstNode.NodeID)
	secondNode := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, secondNode.NodeID)
	key := hostedEnvironment(t, f.pool)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_placements(environment_id,node_id,deployment_generation) VALUES($1,$2,$3)`, key.EnvironmentID, firstNode.NodeID, view.Generation); err != nil {
		t.Fatal(err)
	}
	first, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE session_devices SET native_session_id='retained-native-session' WHERE device_id=$1`, first.DeviceID); err != nil {
		t.Fatal(err)
	}
	first = releaseRetainedFixture(t, f, changes, first)
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, firstNode.NodeID); err != nil {
		t.Fatal(err)
	}
	reserved, err := changes.EnsurePlacement(t.Context(), key, installation)
	if err != nil || reserved.NodeID != secondNode.NodeID {
		t.Fatal(reserved, err)
	}
	// Replayed old cleanup must not release the new reservation, even before Create.
	if _, err := changes.ReleaseAllocation(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	var released bool
	if err := f.pool.QueryRow(t.Context(), `SELECT released_at IS NOT NULL FROM runtime_placements WHERE environment_id=$1`, key.EnvironmentID).Scan(&released); err != nil || released {
		t.Fatal("late release removed reservation", released, err)
	}
	owners := make([]deployment.Allocation, 6)
	errs := make([]error, len(owners))
	var wg sync.WaitGroup
	for i := range owners {
		wg.Go(func() {
			owners[i], errs[i] = changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
		})
	}
	wg.Wait()
	fresh := 0
	for i, owner := range owners {
		if errs[i] != nil || owner.ID != owners[0].ID || owner.NodeID != secondNode.NodeID {
			t.Fatal(owner, errs[i])
		}
		if !owner.Replayed {
			fresh++
		}
	}
	second := owners[0]
	if fresh != 1 || second.ID == first.ID || second.DeviceID == first.DeviceID {
		t.Fatal("replacement reused identity", fresh, first, second)
	}
	var device, native string
	if err := f.pool.QueryRow(t.Context(), `SELECT device_id,native_session_id FROM session_devices WHERE session_id=$1`, second.SessionID).Scan(&device, &native); err != nil || device != second.DeviceID || native != "retained-native-session" {
		t.Fatal(device, native, err)
	}
	for name, write := range map[string]func() (deployment.Allocation, error){"cleanup": func() (deployment.Allocation, error) { return changes.RequestCleanup(t.Context(), first) }, "release": func() (deployment.Allocation, error) { return changes.ReleaseAllocation(t.Context(), first) }, "observe": func() (deployment.Allocation, error) { return changes.ObserveRunning(t.Context(), first) }} {
		if _, err := write(); !errors.Is(err, deployment.ErrAllocationConflict) {
			t.Fatal(name, "accepted stale owner", err)
		}
	}
	var history, current, authority int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER(WHERE state<>'released') FROM runtime_allocations WHERE environment_id=$1`, key.EnvironmentID).Scan(&history, &current); err != nil || history != 2 || current != 1 {
		t.Fatal(history, current, err)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_device_authority WHERE id=$1`, first.DeviceID).Scan(&authority); err != nil || authority != 0 {
		t.Fatal("old credential still authorized", authority, err)
	}
	var oldNode string
	if err := f.pool.QueryRow(t.Context(), `SELECT node_id FROM runtime_allocations WHERE id=$1`, first.ID).Scan(&oldNode); err != nil || oldNode != firstNode.NodeID {
		t.Fatal("historical node changed", oldNode, err)
	}
	nodes, err := f.adapter.Nodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.ID == secondNode.NodeID && (node.Active != 1 || node.Retained != 1) {
			t.Fatal("history counted as compute", node)
		}
	}
	// The database independently rejects a second live owner, even outside the domain transaction.
	otherDevice := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO devices(id,tenant_id,name,credential_hash) VALUES($1,$2,'fixture',$3)`, otherDevice, key.TenantID, credentialHash()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,node_id,deployment_generation) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), key.EnvironmentID, otherDevice, installation, secondNode.NodeID, view.Generation); err == nil {
		t.Fatal("database accepted two current allocations")
	}
}

func TestReplacementRefusesUnsettledOwnershipAndUnknownHistory(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, node.NodeID)
	key := hostedEnvironment(t, f.pool)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_placements(environment_id,node_id,deployment_generation) VALUES($1,$2,$3)`, key.EnvironmentID, node.NodeID, view.Generation); err != nil {
		t.Fatal(err)
	}
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changes.EnsurePlacement(t.Context(), key, installation); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("unknown writer admitted replacement", err)
	}
	replay, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil || !replay.Replayed || replay.ID != owner.ID {
		t.Fatal("unknown Create was replayed", replay, err)
	}
	owner = releaseRetainedFixture(t, f, changes, owner)
	for _, raw := range []string{`[]`, `[{"kind":"codex","available":true,"capabilities":{}}]`, `[{"kind":"codex","available":false,"capabilities":{"retained_native_history":true}}]`, `[{"kind":"different","available":true,"capabilities":{"retained_native_history":true}}]`} {
		if _, err := f.pool.Exec(t.Context(), `UPDATE devices SET supported_agent_kinds=$2 WHERE id=$1`, owner.DeviceID, raw); err != nil {
			t.Fatal(err)
		}
		if allowed, err := f.adapter.RetainedNativeHistory(t.Context(), key); err != nil || allowed {
			t.Fatal("unknown history admitted", raw, allowed, err)
		}
		if _, err := changes.EnsurePlacement(t.Context(), key, installation); !errors.Is(err, deployment.ErrAllocationConflict) {
			t.Fatal("unknown history reserved replacement", err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{"retained_native_history":true}}]',revoked_at=NULL WHERE id=$1`, owner.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.EnsurePlacement(t.Context(), key, installation); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("old device still had authority", err)
	}
}

func TestTenRetainedWorkspacesShareThreeComputeSlots(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 3, MaxRetained: 3})
	f.connect(t, node.NodeID)
	keys := make([]deployment.AllocationKey, 10)
	for i := range keys {
		key := hostedEnvironment(t, f.pool)
		keys[i] = key
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_placements(environment_id,node_id,deployment_generation) VALUES($1,$2,$3)`, key.EnvironmentID, node.NodeID, view.Generation); err != nil {
			t.Fatal(err)
		}
		owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
		if err != nil {
			t.Fatal(err)
		}
		releaseRetainedFixture(t, f, changes, owner)
	}
	check := func(active, retained int64) {
		t.Helper()
		nodes, err := f.adapter.Nodes(t.Context())
		if err != nil || len(nodes) != 1 {
			t.Fatal(nodes, err)
		}
		if nodes[0].Active != active || nodes[0].Retained != retained {
			t.Fatal("incorrect compute accounting", nodes[0])
		}
		var objects int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM environment_workspaces WHERE state='ready'`).Scan(&objects); err != nil || objects != 10 {
			t.Fatal("retained filesystem count", objects, err)
		}
	}
	check(0, 0)
	owners := make([]deployment.Allocation, 3)
	for i := range owners {
		if _, err := changes.EnsurePlacement(t.Context(), keys[i], installation); err != nil {
			t.Fatal(err)
		}
		owner, err := changes.ReserveAllocation(t.Context(), keys[i], installation, credentialHash())
		if err != nil {
			t.Fatal(err)
		}
		owners[i] = owner
	}
	check(3, 3)
	if _, err := changes.EnsurePlacement(t.Context(), keys[3], installation); err == nil {
		t.Fatal("fourth demand exceeded capacity")
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_allocations WHERE environment_id=$1`, keys[3].EnvironmentID).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected demand created allocation", count, err)
	}
	check(3, 3)
	releaseRetainedFixture(t, f, changes, owners[0])
	check(2, 2)
	if _, err := changes.EnsurePlacement(t.Context(), keys[3], installation); err != nil {
		t.Fatal(err)
	}
	// A committed reservation is itself retry demand for live file access.
	pending, err := f.adapter.UnallocatedEnvironments(t.Context(), node.NodeID, "")
	if err != nil || len(pending) != 1 || pending[0].ID != keys[3].EnvironmentID {
		t.Fatal("reservation lost without pending input", pending, err)
	}
	if _, err := changes.ReserveAllocation(t.Context(), keys[3], installation, credentialHash()); err != nil {
		t.Fatal(err)
	}
	check(3, 3)
}

func TestResetArchivesRetainedSessionBeforeClearingDeployment(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, node.NodeID)
	retained := make([]deployment.Allocation, 37)
	for i := range retained {
		key := hostedEnvironment(t, f.pool)
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_placements(environment_id,node_id,deployment_generation) VALUES($1,$2,$3)`, key.EnvironmentID, node.NodeID, view.Generation); err != nil {
			t.Fatal(err)
		}
		owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
		if err != nil {
			t.Fatal(err)
		}
		retained[i] = releaseRetainedFixture(t, f, changes, owner)
	}
	target := retained[0]
	// Only the exact latest ownership in this reset's installation and boundary counts.
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET provider_key=$2 WHERE id=$1`, retained[1].ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	futureDevice := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO devices(id,tenant_id,name,credential_hash,environment_id,revoked_at) VALUES($1,$2,'future', $3,$4,clock_timestamp())`, futureDevice, retained[2].TenantID, credentialHash(), retained[2].EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,node_id,deployment_generation,state,create_settled,released_at) VALUES($1,$2,$3,$4,$5,$6,'released',true,clock_timestamp())`, uuid.NewString(), retained[2].EnvironmentID, futureDevice, installation, node.NodeID, view.Generation+1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET status='expired' WHERE id=$1`, retained[3].EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if err := changes.StartReset(admin(t), installation, deployment.ResetRequest{ExpectedGeneration: view.Generation, Clear: deployment.ResetForce}); err != nil {
		t.Fatal(err)
	}
	started, err := f.service.View(t.Context())
	if err != nil || started.Reset == nil {
		t.Fatal(started, err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE sessions SET created_at=$2 WHERE id=$1`, retained[4].SessionID, started.Reset.RequestedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	candidates, err := f.adapter.ResetSessions(t.Context(), "", true)
	if err != nil || len(candidates) != 32 {
		t.Fatal("reset scope", candidates, err)
	}
	snapshot, err := f.service.View(t.Context())
	if err != nil || snapshot.Resources.Pending != 33 {
		t.Fatal("retained reset work missing from completion gate", snapshot.Resources, err)
	}
	for _, candidate := range candidates {
		for _, excluded := range retained[1:5] {
			if candidate.SessionID == excluded.SessionID {
				t.Fatal("out-of-scope history entered reset", candidate)
			}
		}
	}
	var inUse *deployment.InUseError
	if _, err := changes.CompleteReset(t.Context(), installation, view.Generation, started.Reset.RequestedAt); !errors.As(err, &inUse) {
		t.Fatal("reset completed before archive", err)
	}
	archive := func(candidates []deployment.ResetSession) {
		t.Helper()
		for _, candidate := range candidates {
			if _, err := f.pool.Exec(t.Context(), `INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,$2,$3)`, candidate.TenantID, uuid.NewString(), uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), `INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'retained',$2,'service_account','fixture')`, uuid.NewString(), candidate.TenantID); err != nil {
				t.Fatal(err)
			}
			if _, err := changes.ArchiveResetSession(t.Context(), candidate.TenantID, candidate.SessionID, view.Generation, started.Reset.RequestedAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	archive(candidates)
	remaining, err := f.adapter.ResetSessions(t.Context(), "", true)
	if err != nil || len(remaining) != 1 {
		t.Fatal("second reset page", remaining, err)
	}
	snapshot, err = f.service.View(t.Context())
	if err != nil || snapshot.Resources.Pending != 1 {
		t.Fatal("second page did not block completion", snapshot.Resources, err)
	}
	if _, err := changes.CompleteReset(t.Context(), installation, view.Generation, started.Reset.RequestedAt); !errors.As(err, &inUse) {
		t.Fatal("reset skipped second page", err)
	}
	archive(remaining)
	committed, err := changes.CompleteReset(t.Context(), installation, view.Generation, started.Reset.RequestedAt)
	if err != nil {
		t.Fatal(err)
	}
	next, err := changes.Initialize(admin(t), installation, sandbox.Selection{Provider: "microsandbox", ExpectedGeneration: committed, DeploymentSpec: retainedSpecification()})
	if err != nil {
		t.Fatal(err)
	}
	nextNode := f.enroll(t, next, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, nextNode.NodeID)
	if _, err := changes.EnsurePlacement(t.Context(), target.Key(), installation); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("reset Session revived in new deployment", err)
	}
	var status, fsState string
	if err := f.pool.QueryRow(t.Context(), `SELECT e.status,w.state FROM environments e JOIN environment_workspaces w ON w.environment_id=e.id WHERE e.id=$1`, target.EnvironmentID).Scan(&status, &fsState); err != nil || status != "expired" || fsState != "ready" {
		t.Fatal("archive lost files or kept Session executable", status, fsState, err)
	}
}

func retainedSpecification() sandbox.DeploymentSpec {
	spec := testSpecification("microsandbox")
	spec.Resources.RootDiskMiB = 8192
	spec.Resources.EnvironmentDiskMiB = 0
	spec.Workspace = &workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
	return spec
}

func TestReplacementWaitsForCompatibleReadyGenerationWithoutReservingOwnedNode(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, owned := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
	oldNode := f.enroll(t, owned, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, oldNode.NodeID)
	external, err := changes.Update(admin(t), installation, sandbox.Selection{Provider: "microsandbox", ExpectedGeneration: owned.Generation, DeploymentSpec: retainedSpecification()})
	if err != nil {
		t.Fatal(err)
	}
	newNode := f.enroll(t, external, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, newNode.NodeID)
	key := hostedEnvironment(t, f.pool)
	if _, err = f.pool.Exec(t.Context(), `INSERT INTO runtime_placements(environment_id,node_id,deployment_generation) VALUES($1,$2,$3)`, key.EnvironmentID, newNode.NodeID, external.Generation); err != nil {
		t.Fatal(err)
	}
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	releaseRetainedFixture(t, f, changes, owner)
	if _, err = f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, newNode.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = changes.EnsurePlacement(t.Context(), key, installation); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("owned generation admitted retained FS", err)
	}
	var released bool
	if err = f.pool.QueryRow(t.Context(), `SELECT released_at IS NOT NULL FROM runtime_placements WHERE environment_id=$1`, key.EnvironmentID).Scan(&released); err != nil || !released {
		t.Fatal("incompatible placement consumed capacity", released, err)
	}
	f.connect(t, newNode.NodeID)
	reserved, err := changes.EnsurePlacement(t.Context(), key, installation)
	if err != nil || reserved.NodeID != newNode.NodeID {
		t.Fatal("compatible capacity did not recover demand", reserved, err)
	}
	fresh, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil || fresh.ID == owner.ID || fresh.NodeID != newNode.NodeID {
		t.Fatal(fresh, err)
	}
}
