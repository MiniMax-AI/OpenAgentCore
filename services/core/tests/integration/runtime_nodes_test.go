package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// managerNode is the deployment managerFixture sets up: installation
// InstallationID runs Docker on the one online node NodeID.
type managerNode struct{ InstallationID, NodeID string }

func managerFixture(t *testing.T, active, retained int) (*Store, *Store, managerNode) {
	t.Helper()
	s, w, view, _ := webSpecificationFixture(t, "docker")
	node := enrollNode(t, s, view, deployment.Capacity{MaxActive: active, MaxRetained: retained})
	return s, w, managerNode{InstallationID: view.InstallationID, NodeID: node.NodeID}
}
func onlineManagerNode(t *testing.T, s *Store, id string) string {
	t.Helper()
	connection := uuid.NewString()
	nodes := deploymentService(t, s)
	if err := nodes.ConnectNode(t.Context(), id, connection, managerEpoch(t, s)); err != nil {
		t.Fatal(err)
	}
	if err := nodes.Heartbeat(t.Context(), id, connection, managerEpoch(t, s), deployment.NodeHealth{ProviderReady: true}); err != nil {
		t.Fatal(err)
	}
	return connection
}
func managerSessionInput(key string) sessions.CreateSession {
	return environmentInput(key, "openai_hosted", "/workspace")
}

// createSessionOnNode steers automatic placement in multi-node tests: only node
// stays provider-ready while the Session is created.
func createSessionOnNode(t *testing.T, s *Store, tenant string, input sessions.CreateSession, node string) (sessions.Session, error) {
	t.Helper()
	// Use the same authenticated readiness observations as the scheduler. The
	// compatibility provider_ready column alone is not admission authority.
	type presence struct {
		id, connection string
		epoch          uint64
	}
	rows, err := s.pool.Query(t.Context(), "SELECT id::text, connection_id::text, connected_epoch FROM runtime_nodes WHERE provider_ready AND id<>$1 AND connection_id IS NOT NULL AND removed_at IS NULL", node)
	if err != nil {
		t.Fatal(err)
	}
	var others []presence
	for rows.Next() {
		var value presence
		if err := rows.Scan(&value.id, &value.connection, &value.epoch); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		others = append(others, value)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	nodes := deploymentService(t, s)
	for _, value := range others {
		if err := nodes.Heartbeat(t.Context(), value.id, value.connection, value.epoch, deployment.NodeHealth{ProviderReady: false}); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, value := range others {
			if err := nodes.Heartbeat(context.WithoutCancel(t.Context()), value.id, value.connection, value.epoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
				t.Fatal(err)
			}
		}
	}()
	return s.CreateSession(t.Context(), tenant, input)
}

type sessionPlacement struct {
	NodeID    string
	Available bool
}

// sessionRuntimePlacement reads the node a Session was placed on.
func sessionRuntimePlacement(ctx context.Context, s *Store, tenant, session string) (sessionPlacement, error) {
	value, err := sessionAdapter(s).GetSession(ctx, tenant, session)
	if err != nil {
		return sessionPlacement{}, err
	}
	if value.Environment == nil {
		return sessionPlacement{}, sessions.ErrNotFound
	}
	id, err := parseID(value.Environment.ID)
	if err != nil {
		return sessionPlacement{}, err
	}
	p, err := s.queries.GetRuntimePlacement(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionPlacement{}, sessions.ErrNotFound
	}
	placement := sessionPlacement{Available: p.Available && !p.ReleasedAt.Valid}
	if p.NodeID.Valid {
		placement.NodeID = uuid.UUID(p.NodeID.Bytes).String()
	}
	return placement, err
}
func TestRuntimeNodesAtomicPlacementAndRetry(t *testing.T) {
	s, _, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan sessions.Session, 16)
	failures := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
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
		} else if !errors.Is(err, placement.ErrNodeUnavailable) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("overbooked node", successes)
	}
	var retained sessions.Session
	for session := range results {
		if session.ID != "" {
			retained = session
		}
	}
	service := deploymentService(t, s)
	nodes, err := service.ListNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].Active != 1 || nodes[0].Retained != 1 || nodes[0].Reserved != 1 {
		t.Fatal(nodes, err)
	}
	if err := service.RemoveNode(t.Context(), d.NodeID); !errors.Is(err, deployment.ErrNodeInUse) {
		t.Fatal("removed pending placement", err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: retained.ID}); err != nil {
		t.Fatal(err)
	}
	input := managerSessionInput("retry")
	first, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.NodeID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || replay.ID != first.ID {
		t.Fatal("offline retry changed Session", replay, err)
	}
	if _, err := sessionRuntimePlacement(t.Context(), s, uuid.NewString(), first.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign placement leaked", err)
	}
	placement, err := sessionRuntimePlacement(t.Context(), s, tenant, first.ID)
	if err != nil || placement.NodeID != d.NodeID || placement.Available {
		t.Fatal(placement, err)
	}
}
func TestRuntimeNodesEnrollmentAndEpoch(t *testing.T) {
	s, w, d := managerFixture(t, 2, 4)
	nodes := deploymentService(t, s)
	token, err := EnrollmentTestToken(nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 2, MaxRetained: 4}))
	if err != nil {
		t.Fatal(err)
	}
	input := deployment.Enrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: uuid.NewString(), Credential: strings.Repeat("x", 64), Name: "remote", Provider: "microsandbox", BackendFingerprint: strings.Repeat("b", 64), CoreURL: s.placement.PublicURL()}
	if _, err := nodes.Enroll(t.Context(), token, input); !errors.Is(err, deployment.ErrInvalidInput) {
		t.Fatal("mixed provider accepted", err)
	}
	input.Provider = "docker"
	enrolled, err := nodes.Enroll(t.Context(), token, input)
	if err != nil || enrolled.InstallationID != d.InstallationID {
		t.Fatal(enrolled, err)
	}
	if _, err := nodes.Enroll(t.Context(), token, input); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("enrollment token reused", err)
	}
	if _, err := nodes.AuthenticateNode(t.Context(), input.NodeID, input.Credential); err != nil {
		t.Fatal("lost response cannot recover", err)
	}
	connection := onlineManagerNode(t, s, input.NodeID)
	if err := nodes.DisconnectNode(t.Context(), input.NodeID, uuid.NewString(), managerEpoch(t, s)); err != nil {
		t.Fatal(err)
	}
	current, err := nodes.ListNodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range current {
		if n.ID == input.NodeID && !n.Online {
			t.Fatal("stale disconnect fenced current connection")
		}
	}
	epoch := managerEpoch(t, s)
	if err := deploymentExecution(t, w).Claim(t.Context(), d.InstallationID); err != nil {
		t.Fatal(err)
	}
	if next := managerEpoch(t, s); next != epoch+1 {
		t.Fatal(next)
	}
	if err := nodes.Heartbeat(t.Context(), input.NodeID, connection, epoch, deployment.NodeHealth{ProviderReady: true}); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("old epoch heartbeat revived node", err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput("stale")); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("stale node admitted", err)
	}
	onlineManagerNode(t, s, d.NodeID)
	if err := nodes.RemoveNode(t.Context(), input.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := nodes.AuthenticateNode(t.Context(), input.NodeID, input.Credential); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("removed node credential accepted", err)
	}
}
func TestRuntimeNodesRetention(t *testing.T) {
	s, w, next := managerFixture(t, 2, 2)
	nodes := deploymentService(t, s)
	tenant := uuid.NewString()
	first, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: first.Environment.ID}, next.InstallationID, runtimedevice.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err := nodes.RemoveNode(t.Context(), next.NodeID); !errors.Is(err, deployment.ErrNodeInUse) {
		t.Fatal(err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: pending.ID}); err != nil {
		t.Fatal(err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: first.ID}); err != nil {
		t.Fatal(err)
	}
	retained, err = deploymentExecution(t, w).RequestCleanup(t.Context(), retained)
	if err != nil {
		t.Fatal(err)
	}
	if err := nodes.RemoveNode(t.Context(), next.NodeID); !errors.Is(err, deployment.ErrNodeInUse) {
		t.Fatal("unknown cleanup released node", err)
	}
	retained, err = deploymentExecution(t, w).SettleCreation(t.Context(), retained)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).ReleaseAllocation(t.Context(), retained); err != nil {
		t.Fatal(err)
	}
	if err := nodes.RemoveNode(t.Context(), next.NodeID); err != nil {
		t.Fatal("released node cannot be removed", err)
	}
}
func TestRuntimeNodesRestoreAndCreationShareCapacity(t *testing.T) {
	s, w, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput("first"))
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID}, d.InstallationID, runtimedevice.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET state='running',create_settled=true,compute_phase='suspended',compute_retained_until=clock_timestamp()+interval '1 hour',compute_state=$2::jsonb WHERE id=$1", allocation.ID, json.RawMessage(`{"snapshot":{"id":"owned"}}`)); err != nil {
		t.Fatal(err)
	}
	allocation, err = deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := deploymentExecution(t, w).SetCompute(t.Context(), allocation, "restoring", json.RawMessage(`{"target":{"id":"restore"}}`), &until, 0)
		results <- err
	}()
	go func() {
		<-start
		_, err := s.CreateSession(t.Context(), tenant, managerSessionInput("second"))
		results <- err
	}()
	close(start)
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if !errors.Is(err, placement.ErrNodeUnavailable) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("restore and creation overbooked", success)
	}
	nodes, err := deploymentService(t, s).ListNodes(t.Context())
	if err != nil || nodes[0].Active != 1 {
		t.Fatal(nodes, err)
	}
}

func managerEpoch(t *testing.T, s *Store) uint64 {
	t.Helper()
	epoch, err := deploymentStore(s).OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}
func TestRuntimeNodesLongOfflineRetainsExactAllocation(t *testing.T) {
	s, w, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput("long-offline"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID}, d.InstallationID, runtimedevice.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = deploymentExecution(t, w).ObserveRunning(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.NodeID); err != nil {
		t.Fatal(err)
	}
	offline, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID})
	if err != nil || offline.Expired || offline.State != "running" {
		t.Fatal("offline treated as destructive expiry", offline, err)
	}
	if err := deploymentService(t, s).RemoveNode(t.Context(), d.NodeID); !errors.Is(err, deployment.ErrNodeInUse) {
		t.Fatal("offline ownership discarded", err)
	}
	onlineManagerNode(t, s, d.NodeID)
	resumed, err := deploymentExecution(t, w).ObserveRunning(t.Context(), offline)
	if err != nil || resumed.ID != owner.ID || resumed.DeviceID != owner.DeviceID || resumed.NodeID != owner.NodeID {
		t.Fatal("reconnect changed instance", resumed, err)
	}
	if _, err := deploymentExecution(t, w).CheckRunning(t.Context(), resumed); err != nil {
		t.Fatal("reconnected allocation stopped running", err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_phase='suspended',compute_state=$2::jsonb,compute_retained_until=clock_timestamp()+interval '1 day' WHERE id=$1", owner.ID, json.RawMessage(`{"snapshot":{"id":"same-snapshot"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.NodeID); err != nil {
		t.Fatal(err)
	}
	retained, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID})
	if err != nil || retained.Expired || string(retained.ComputeState) != `{"snapshot": {"id": "same-snapshot"}}` {
		t.Fatal(retained, err)
	}
	onlineManagerNode(t, s, d.NodeID)
	same, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID})
	if err != nil || same.ID != owner.ID || string(same.ComputeState) != string(retained.ComputeState) {
		t.Fatal("snapshot changed across reconnect", same, err)
	}
	placement, err := sessionRuntimePlacement(t.Context(), s, tenant, session.ID)
	if err != nil || placement.NodeID != d.NodeID {
		t.Fatal(placement, err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: session.Environment.ID})
	if err != nil || !expired.Expired {
		t.Fatal("explicit snapshot retention ignored", expired, err)
	}
}
