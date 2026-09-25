package store

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestManagedSessionArchiveReleasesPendingNodePlacement(t *testing.T) {
	s, _ := newManagedTestStore(t)
	w := executionLease(t, s).Store()
	installation := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	if _, err := w.InitializeSandboxDeployment(t.Context(), installation, SandboxDeploymentSetupRequest{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker", CoreURL: "https://core.example"}); err != nil {
		t.Fatal(err)
	}
	token, _, err := s.CreateRuntimeEnrollment(t.Context(), RuntimeNodeCapacity{MaxActive: 1, MaxRetained: 1})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.NewString()
	if _, err := s.EnrollRuntimeNode(t.Context(), token, RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: nodeID, Name: "Archive fixture", Provider: "docker", Credential: strings.Repeat("x", 64), BackendFingerprint: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, nodeID)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString(), nodeID))
	archiveMaintenance(t, w, installation, true)
	result, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 1)
	if err != nil || result.State != "released" {
		t.Fatal(result, err)
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].Active != 0 || nodes[0].Retained != 0 || nodes[0].Reserved != 0 {
		t.Fatal("unallocated archive retained placement capacity", nodes, err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), nodeID); err != nil {
		t.Fatal("released placement prevented node removal", err)
	}
}

func TestManagedSessionArchiveOrdersConcurrentInput(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	for range 8 {
		archiveMaintenance(t, w, installation, false)
		tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString(), ""))
		archiveMaintenance(t, w, installation, true)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.ReserveEnvironmentInput(t.Context(), tenant, session.ID, "racing-input", []Input{{Kind: "message", Payload: json.RawMessage(`{"text":"racing"}`)}})
			if err != nil && !errors.Is(err, ErrEnvironmentUnavailable) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 1)
			if err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wg.Wait()
		var pending int
		if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1 AND state='pending'", session.ID).Scan(&pending); err != nil || pending != 0 {
			t.Fatal("input survived concurrent archive", pending, err)
		}
		if row, err := s.GetSession(t.Context(), tenant, session.ID); err != nil || row.Environment.Status != "expired" {
			t.Fatal("concurrent input revived Environment", row, err)
		}
	}
}

func TestManagedSessionArchiveRejectsFileManagedDeployment(t *testing.T) {
	s, w, installation := managerFixture(t, 1, 1)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString(), installation.LocalNodeID))
	if _, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 0); !errors.Is(err, ErrSandboxDeploymentConflict) {
		t.Fatal("archive accepted file-managed deployment", err)
	}
}
