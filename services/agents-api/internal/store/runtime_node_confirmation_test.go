package store

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
)

func ActivateRuntimeNodeForTest(t *testing.T, s *Store, id string, active, retained int) {
	t.Helper()
	connection := uuid.NewString()
	epoch, err := s.RuntimeOwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConnectRuntimeNode(t.Context(), id, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatRuntimeNode(t.Context(), id, connection, epoch, nodeCapacityHealth()); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetRuntimeNode(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateRuntimeNode(t.Context(), id, RuntimeNodeUpdate{MaxActive: &active, MaxRetained: &retained, AdmissionState: nodeCapacityPointer("enabled"), ExpectedConfigRevision: current.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
}

func pendingRuntimeNode(t *testing.T, s *Store) (RuntimeEnrollmentToken, RuntimeNodeEnrollment, RuntimeNode) {
	t.Helper()
	receipt, err := s.CreateRuntimeEnrollment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: uuid.NewString(), Credential: strings.Repeat("x", 64), Name: "Pending", Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), MaxActive: 1000000, MaxRetained: 1000000}
	if _, err := s.EnrollRuntimeNode(t.Context(), receipt.Token, input); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetRuntimeNode(t.Context(), input.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	return receipt, input, current
}

func TestRuntimeNodeConfirmationAndSafeReplay(t *testing.T) {
	s, _, d := managerFixture(t, 2, 4)
	_, input, pending := pendingRuntimeNode(t, s)
	if pending.AdmissionState != "pending_confirmation" || pending.Schedulable || pending.MaxActive != 1 || pending.MaxRetained != 1 {
		t.Fatal(pending)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	connection := onlineManagerNode(t, s, input.NodeID)
	for _, selected := range []string{"", input.NodeID} {
		if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString(), selected)); !errors.Is(err, ErrRuntimeNodeUnavailable) {
			t.Fatal("pending node received placement", err)
		}
	}
	if err := s.runtimeManagerTransaction(t.Context(), func(q *sqlc.Queries, _ sqlc.RuntimeDeployment) error {
		id, _ := parseConnectionGeneration(input.NodeID)
		return reserveRuntimeRestore(t.Context(), q, id)
	}); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("pending node restored", err)
	}
	request := RuntimeNodeUpdate{MaxActive: nodeCapacityPointer(3), AdmissionState: nodeCapacityPointer("enabled"), ExpectedConfigRevision: pending.ConfigRevision}
	if _, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, request); !errors.Is(err, ErrRuntimeNodeConfigurationConflict) {
		t.Fatal("unknown health enabled node", err)
	}
	if err := s.HeartbeatRuntimeNode(t.Context(), input.NodeID, connection, managerEpoch(t, s), nodeCapacityHealth()); err != nil {
		t.Fatal(err)
	}
	observed, err := s.GetRuntimeNode(t.Context(), input.NodeID)
	if err != nil || observed.ConfigRevision != pending.ConfigRevision {
		t.Fatal("heartbeat changed config revision", observed, err)
	}
	enabled, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, request)
	if err != nil || !enabled.Schedulable || enabled.MaxActive != 3 || enabled.MaxRetained != 16 || enabled.ConfigRevision == pending.ConfigRevision {
		t.Fatal(enabled, err)
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), input.NodeID, input.Credential); err != nil {
		t.Fatal(err)
	}
	if err := s.DisconnectRuntimeNode(t.Context(), input.NodeID, connection, managerEpoch(t, s)); err != nil {
		t.Fatal(err)
	}
	repeated, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, request)
	if err != nil || repeated.ConfigRevision != enabled.ConfigRevision || repeated.Online {
		t.Fatal("lost response retry failed", repeated, err)
	}
	request.MaxActive = nodeCapacityPointer(4)
	if _, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, request); !errors.Is(err, ErrRuntimeNodeConfigurationConflict) {
		t.Fatal("stale conflicting request accepted", err)
	}
	name := "Saved offline"
	renamed, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, RuntimeNodeUpdate{Name: &name, ExpectedConfigRevision: enabled.ConfigRevision})
	if err != nil || renamed.Name != name || renamed.MaxActive != 3 || renamed.MaxRetained != 16 {
		t.Fatal("partial name patch changed capacities", renamed, err)
	}
	connection = onlineManagerNode(t, s, input.NodeID)
	reconnected, err := s.GetRuntimeNode(t.Context(), input.NodeID)
	if err != nil || reconnected.ConfigRevision != renamed.ConfigRevision || !reconnected.Schedulable {
		t.Fatal("reconnect changed admission", reconnected, err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString(), input.NodeID)); err != nil {
		t.Fatal("confirmed node failed placement", err)
	}
}

func TestRuntimeEnrollmentReceiptsAreExactAndNonSecret(t *testing.T) {
	s, _, _ := managerFixture(t, 2, 4)
	first, err := s.CreateRuntimeEnrollment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, input, _ := pendingRuntimeNode(t, s)
	if first.ID == second.ID || first.ID == first.Token {
		t.Fatal("receipt identity is not independent")
	}
	wait, err := s.GetRuntimeEnrollmentReceipt(t.Context(), first.ID)
	if err != nil || wait.Status != "waiting" || wait.NodeID != nil {
		t.Fatal(wait, err)
	}
	for _, r := range []RuntimeEnrollmentToken{first, second} {
		if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_node_enrollments SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", r.ID); err != nil {
			t.Fatal(err)
		}
	}
	expired, err := s.GetRuntimeEnrollmentReceipt(t.Context(), first.ID)
	if err != nil || expired.Status != "expired" || expired.NodeID != nil {
		t.Fatal(expired, err)
	}
	enrolled, err := s.GetRuntimeEnrollmentReceipt(t.Context(), second.ID)
	if err != nil || enrolled.Status != "enrolled" || enrolled.NodeID == nil || *enrolled.NodeID != input.NodeID {
		t.Fatal(enrolled, err)
	}
	if _, err := s.GetRuntimeEnrollmentReceipt(t.Context(), uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestRuntimeNodeConfirmationRejectsChangedDeploymentAndFreshState(t *testing.T) {
	for _, mode := range []string{"maintenance", "generation", "sample_stale", "insufficient_memory", "over_recommendation"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := managerFixture(t, 2, 4)
			_, input, current := pendingRuntimeNode(t, s)
			connection := onlineManagerNode(t, s, input.NodeID)
			health := nodeCapacityHealth()
			if mode == "sample_stale" {
				health.ObservedAt = nodeCapacityPointer(time.Now().Add(-time.Minute))
			}
			if mode == "insufficient_memory" {
				health.AvailableMemoryBytes = nodeCapacityPointer(int64(1))
			}
			if err := s.HeartbeatRuntimeNode(t.Context(), input.NodeID, connection, managerEpoch(t, s), health); err != nil {
				t.Fatal(err)
			}
			if mode == "maintenance" {
				if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_deployment SET maintenance=true"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "generation" {
				if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_deployment SET generation=generation+1"); err != nil {
					t.Fatal(err)
				}
			}
			active := 3
			if mode == "over_recommendation" {
				active = 1000000
			}
			if _, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, RuntimeNodeUpdate{MaxActive: &active, AdmissionState: nodeCapacityPointer("enabled"), ExpectedConfigRevision: current.ConfigRevision}); !errors.Is(err, ErrRuntimeNodeConfigurationConflict) {
				t.Fatal("unsafe confirmation accepted", err)
			}
			got, err := s.GetRuntimeNode(t.Context(), input.NodeID)
			if err != nil || got.AdmissionState != "pending_confirmation" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestRuntimeNodeCapacityDecreaseSerializesPlacement(t *testing.T) {
	for range 6 {
		s, _, d := managerFixture(t, 2, 4)
		current, err := s.GetRuntimeNode(t.Context(), d.LocalNodeID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString(), d.LocalNodeID)); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		var patchErr, placeErr error
		go func() {
			defer wg.Done()
			<-start
			_, patchErr = s.UpdateRuntimeNode(t.Context(), d.LocalNodeID, RuntimeNodeUpdate{MaxActive: nodeCapacityPointer(1), ExpectedConfigRevision: current.ConfigRevision})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, placeErr = s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString(), d.LocalNodeID))
		}()
		close(start)
		wg.Wait()
		if patchErr == nil && placeErr == nil {
			t.Fatal("placement overbooked decreased capacity")
		}
		if patchErr != nil && !errors.Is(patchErr, ErrRuntimeNodeConfigurationConflict) {
			t.Fatal(patchErr)
		}
		if placeErr != nil && !errors.Is(placeErr, ErrRuntimeNodeUnavailable) {
			t.Fatal(placeErr)
		}
		got, err := s.GetRuntimeNode(t.Context(), d.LocalNodeID)
		if err != nil || got.Active > int64(got.MaxActive) {
			t.Fatal(got, err)
		}
	}
}

func TestRuntimeNodeConcurrentConfirmationCommitsOnce(t *testing.T) {
	s, _, _ := managerFixture(t, 2, 4)
	_, input, current := pendingRuntimeNode(t, s)
	connection := onlineManagerNode(t, s, input.NodeID)
	if err := s.HeartbeatRuntimeNode(t.Context(), input.NodeID, connection, managerEpoch(t, s), nodeCapacityHealth()); err != nil {
		t.Fatal(err)
	}
	request := RuntimeNodeUpdate{MaxActive: nodeCapacityPointer(3), AdmissionState: nodeCapacityPointer("enabled"), ExpectedConfigRevision: current.ConfigRevision}
	var wg sync.WaitGroup
	responses := make(chan RuntimeNode, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, request)
			responses <- n
			failures <- err
		}()
	}
	wg.Wait()
	close(responses)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var revision string
	for got := range responses {
		if revision == "" {
			revision = got.ConfigRevision
		}
		if got.ConfigRevision != revision || got.AdmissionState != "enabled" {
			t.Fatal("duplicate changed configuration", got)
		}
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_deployment SET generation=generation+1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateRuntimeNode(t.Context(), input.NodeID, request); !errors.Is(err, ErrRuntimeNodeConfigurationConflict) {
		t.Fatal("old successful confirmation replayed across deployment change", err)
	}
}

func TestRuntimeNodeCapacityDecreasePreservesRetainedResources(t *testing.T) {
	s, w, d := managerFixture(t, 4, 4)
	allocation := lifecycleTestAllocation(t, s, w, d, d.LocalNodeID)
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_phase='suspended' WHERE id=$1", allocation.ID); err != nil {
		t.Fatal(err)
	}
	lifecycleTestSession(t, s, d.LocalNodeID)
	current, err := s.GetRuntimeNode(t.Context(), d.LocalNodeID)
	if err != nil || current.Active != 1 || current.Retained != 2 {
		t.Fatal(current, err)
	}
	request := RuntimeNodeUpdate{MaxActive: nodeCapacityPointer(1), MaxRetained: nodeCapacityPointer(1), ExpectedConfigRevision: current.ConfigRevision}
	if _, err := s.UpdateRuntimeNode(t.Context(), d.LocalNodeID, request); !errors.Is(err, ErrRuntimeNodeConfigurationConflict) {
		t.Fatal("retained resources overcommitted", err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	request.MaxRetained = nodeCapacityPointer(2)
	decreased, err := s.UpdateRuntimeNode(t.Context(), d.LocalNodeID, request)
	if err != nil || decreased.MaxActive != 1 || decreased.MaxRetained != 2 {
		t.Fatal("safe offline decrease rejected", decreased, err)
	}
}

func TestRuntimeNodeRestorePreservesAdmittedWorkInMaintenance(t *testing.T) {
	s, w, d := managerFixture(t, 1, 4)
	_, pending, _ := pendingRuntimeNode(t, s)
	onlineManagerNode(t, s, pending.NodeID)
	allocation := lifecycleTestAllocation(t, s, w, d, d.LocalNodeID)
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET state='running',create_settled=true,compute_phase='suspended',compute_retained_until=clock_timestamp()+interval '1 hour',compute_state=$2::jsonb WHERE id=$1", allocation.ID, json.RawMessage(`{"snapshot":{"id":"owned"}}`)); err != nil {
		t.Fatal(err)
	}
	allocation, err := s.GetRuntimeAllocation(t.Context(), allocation.TenantID, allocation.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_deployment SET maintenance=true"); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if _, err := w.SetRuntimeCompute(t.Context(), allocation, "restoring", json.RawMessage(`{"target":{"id":"restore"}}`), &until, 0); err != nil {
		t.Fatal("maintenance stopped admitted restore", err)
	}
	if err := s.runtimeManagerTransaction(t.Context(), func(q *sqlc.Queries, _ sqlc.RuntimeDeployment) error {
		id, _ := parseConnectionGeneration(pending.NodeID)
		return reserveRuntimeRestore(t.Context(), q, id)
	}); !errors.Is(err, ErrRuntimeNodeUnavailable) {
		t.Fatal("maintenance allowed pending restore", err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), managerSessionInput(uuid.NewString(), d.LocalNodeID)); !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatal("maintenance allowed fresh placement", err)
	}
}
