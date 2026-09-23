package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

func managerFixture(t *testing.T, active, retained int) (*Store, *Store, RuntimeDeployment) {
	t.Helper()
	s, _ := newManagedTestStore(t)
	w := executionLease(t, s).Store()
	d := deploymentSelection()
	d.ProviderKind = "docker"
	d.LocalNodeID = uuid.NewString()
	d.LocalCredentialSHA256 = device.HashCredential("local-node-credential")
	d.LocalMaxActive = active
	d.LocalMaxRetained = retained
	deploymentConfigure(t, w, &d)
	onlineManagerNode(t, s, d.LocalNodeID)
	return s, w, d
}
func onlineManagerNode(t *testing.T, s *Store, id string) string {
	t.Helper()
	connection := uuid.NewString()
	if err := s.ConnectRuntimeNode(t.Context(), id, connection, managerEpoch(t, s)); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatRuntimeNode(t.Context(), id, connection, managerEpoch(t, s), RuntimeNodeHealth{ProviderReady: true}); err != nil {
		t.Fatal(err)
	}
	return connection
}
func managerSessionInput(key, node string) CreateSessionInput {
	input := environmentInput(key, "openai_hosted", "/workspace")
	input.SandboxNodeID = node
	return input
}
func TestRuntimeNodesAtomicPlacementAndRetry(t *testing.T) {
	s, _, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan Session, 16)
	failures := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString(), ""))
			results <- result
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	successes := 0
	for err := range failures {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrRuntimeNodeUnavailable) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("overbooked node", successes)
	}
	var retained Session
	for session := range results {
		if session.ID != "" {
			retained = session
		}
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].Active != 1 || nodes[0].Retained != 1 || nodes[0].Reserved != 1 {
		t.Fatal(nodes, err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), d.LocalNodeID); !errors.Is(err, ErrRuntimeNodeInUse) {
		t.Fatal("removed pending placement", err)
	}
	if err := s.DeleteSession(t.Context(), tenant, retained.ID); err != nil {
		t.Fatal(err)
	}
	input := managerSessionInput("retry", d.LocalNodeID)
	first, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || replay.ID != first.ID {
		t.Fatal("offline retry changed Session", replay, err)
	}
	input.SandboxNodeID = uuid.NewString()
	if _, err := s.CreateSession(t.Context(), tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("retry changed selector", err)
	}
	if _, err := s.GetSessionRuntimePlacement(t.Context(), uuid.NewString(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign placement leaked", err)
	}
	placement, err := s.GetSessionRuntimePlacement(t.Context(), tenant, first.ID)
	if err != nil || placement.NodeID != d.LocalNodeID || placement.Available {
		t.Fatal(placement, err)
	}
}
func TestRuntimeNodesEnrollmentAndEpoch(t *testing.T) {
	s, w, d := managerFixture(t, 2, 4)
	token, _, err := s.CreateRuntimeEnrollment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := RuntimeNodeEnrollment{NodeID: uuid.NewString(), Credential: strings.Repeat("x", 64), Name: "remote", Provider: "microsandbox", BackendFingerprint: strings.Repeat("b", 64), MaxActive: 2, MaxRetained: 4}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("mixed provider accepted", err)
	}
	input.Provider = "docker"
	enrolled, err := s.EnrollRuntimeNode(t.Context(), token, input)
	if err != nil || enrolled.InstallationID != d.InstallationID {
		t.Fatal(enrolled, err)
	}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, input); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("enrollment token reused", err)
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), input.NodeID, input.Credential); err != nil {
		t.Fatal("lost response cannot recover", err)
	}
	connection := onlineManagerNode(t, s, input.NodeID)
	if err := s.DisconnectRuntimeNode(t.Context(), input.NodeID, uuid.NewString(), managerEpoch(t, s)); err != nil {
		t.Fatal(err)
	}
	current, err := s.ListRuntimeNodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range current {
		if n.ID == input.NodeID && !n.Online {
			t.Fatal("stale disconnect fenced current connection")
		}
	}
	epoch, err := s.RuntimeOwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	deploymentConfigure(t, w, &d)
	next, err := s.RuntimeOwnerEpoch(t.Context())
	if err != nil || next != epoch+1 {
		t.Fatal(next, err)
	}
	if err := s.HeartbeatRuntimeNode(t.Context(), input.NodeID, connection, epoch, RuntimeNodeHealth{ProviderReady: true}); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("old epoch heartbeat revived node", err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput("stale", "")); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("stale node admitted", err)
	}
	onlineManagerNode(t, s, d.LocalNodeID)
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput("manual-offline", input.NodeID)); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("manual placement fell back", err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), input.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), input.NodeID, input.Credential); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("removed node credential accepted", err)
	}
}
func TestRuntimeNodesLegacyAdoptionAndRetention(t *testing.T) {
	s, _ := newManagedTestStore(t)
	w := executionLease(t, s).Store()
	d := deploymentSelection()
	deploymentConfigure(t, w, &d)
	tenant := uuid.NewString()
	first, environment := localEnvironment(t, s, tenant)
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, d.InstallationID, device.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := localEnvironment(t, s, tenant)
	next := d
	next.ProviderKind = "docker"
	next.LocalNodeID = uuid.NewString()
	next.LocalCredentialSHA256 = device.HashCredential("node")
	next.LocalMaxActive = 1
	next.LocalMaxRetained = 1
	wrong := next
	wrong.BackendFingerprint = strings.Repeat("c", 64)
	if err := w.ConfigureRuntimeDeployment(t.Context(), &wrong, nil); err == nil {
		t.Fatal("adopted wrong backend")
	}
	if err := w.ConfigureRuntimeDeployment(t.Context(), &next, func(context.Context, RuntimeAllocation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	retained, err := s.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
	if err != nil || retained.NodeID != next.LocalNodeID || retained.ProviderKey != d.InstallationID || retained.ID != owner.ID {
		t.Fatal("adoption lost identity", retained, err)
	}
	for _, session := range []Session{first, pending} {
		placement, err := s.GetSessionRuntimePlacement(t.Context(), tenant, session.ID)
		if err != nil || placement.NodeID != next.LocalNodeID {
			t.Fatal("legacy Session reassigned", placement, err)
		}
	}
	if err := s.RemoveRuntimeNode(t.Context(), next.LocalNodeID); !errors.Is(err, ErrRuntimeNodeInUse) {
		t.Fatal(err)
	}
	if err := s.DeleteSession(t.Context(), tenant, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(t.Context(), tenant, first.ID); err != nil {
		t.Fatal(err)
	}
	retained, err = w.RequestRuntimeCleanup(t.Context(), retained)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), next.LocalNodeID); !errors.Is(err, ErrRuntimeNodeInUse) {
		t.Fatal("unknown cleanup released node", err)
	}
	retained, err = w.SettleRuntimeCreation(t.Context(), retained)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReleaseRuntimeAllocation(t.Context(), retained); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), next.LocalNodeID); !errors.Is(err, ErrRuntimeLocalNodeConfigured) {
		t.Fatal("configured local node was removed", err)
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), next.LocalNodeID, "node"); err != nil {
		t.Fatal("rejected removal changed local credentials", err)
	}
	next.Maintenance = true
	deploymentConfigure(t, w, &next)
	detached := next
	detached.LocalNodeID = ""
	detached.LocalCredentialSHA256 = ""
	detached.LocalMaxActive, detached.LocalMaxRetained = 0, 0
	detached.BackendFingerprint = strings.Repeat("b", 64)
	deploymentConfigure(t, w, &detached)
	if err := s.RemoveRuntimeNode(t.Context(), next.LocalNodeID); err != nil {
		t.Fatal("detached resolved node cannot be removed", err)
	}
}
func TestRuntimeNodesRestoreAndCreationShareCapacity(t *testing.T) {
	s, w, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput("first", d.LocalNodeID))
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, d.InstallationID, device.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET state='running',create_settled=true,compute_phase='suspended',compute_retained_until=clock_timestamp()+interval '1 hour',compute_state=$2::jsonb WHERE id=$1", allocation.ID, json.RawMessage(`{"snapshot":{"id":"owned"}}`)); err != nil {
		t.Fatal(err)
	}
	allocation, err = s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := w.SetRuntimeCompute(t.Context(), allocation, "restoring", json.RawMessage(`{"target":{"id":"restore"}}`), &until, 0)
		results <- err
	}()
	go func() {
		<-start
		_, err := s.CreateSession(t.Context(), tenant, managerSessionInput("second", d.LocalNodeID))
		results <- err
	}()
	close(start)
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if !errors.Is(err, ErrRuntimeNodeUnavailable) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("restore and creation overbooked", success)
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || nodes[0].Active != 1 {
		t.Fatal(nodes, err)
	}
}

func managerEpoch(t *testing.T, s *Store) uint64 {
	t.Helper()
	epoch, err := s.RuntimeOwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}
func TestRuntimeNodesLongOfflineRetainsExactAllocation(t *testing.T) {
	s, w, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput("long-offline", d.LocalNodeID))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, d.InstallationID, device.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.ObserveRuntimeRunning(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET kept_at=clock_timestamp()-interval '2 days' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	offline, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || offline.Expired || offline.State != "running" {
		t.Fatal("offline treated as destructive expiry", offline, err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), d.LocalNodeID); !errors.Is(err, ErrRuntimeNodeInUse) {
		t.Fatal("offline ownership discarded", err)
	}
	changed := d
	changed.LocalNodeID = uuid.NewString()
	if err := w.ConfigureRuntimeDeployment(t.Context(), &changed, nil); err == nil {
		t.Fatal("lost local state created replacement identity")
	}
	onlineManagerNode(t, s, d.LocalNodeID)
	resumed, err := w.ObserveRuntimeRunning(t.Context(), offline)
	if err != nil || resumed.ID != owner.ID || resumed.DeviceID != owner.DeviceID || resumed.NodeID != owner.NodeID {
		t.Fatal("reconnect changed instance", resumed, err)
	}
	if _, err := w.KeepRuntimeAllocation(t.Context(), resumed); err != nil {
		t.Fatal("offline observation lease could not renew", err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_phase='suspended',compute_state=$2::jsonb,compute_retained_until=clock_timestamp()+interval '1 day' WHERE id=$1", owner.ID, json.RawMessage(`{"snapshot":{"id":"same-snapshot"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	retained, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || retained.Expired || string(retained.ComputeState) != `{"snapshot": {"id": "same-snapshot"}}` {
		t.Fatal(retained, err)
	}
	onlineManagerNode(t, s, d.LocalNodeID)
	same, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || same.ID != owner.ID || string(same.ComputeState) != string(retained.ComputeState) {
		t.Fatal("snapshot changed across reconnect", same, err)
	}
	placement, err := s.GetSessionRuntimePlacement(t.Context(), tenant, session.ID)
	if err != nil || placement.NodeID != d.LocalNodeID {
		t.Fatal(placement, err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || !expired.Expired {
		t.Fatal("explicit snapshot retention ignored", expired, err)
	}
}
