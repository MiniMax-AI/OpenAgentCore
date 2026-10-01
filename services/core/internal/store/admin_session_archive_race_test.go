package store

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestManagedSessionArchiveReleasesPendingNodePlacement(t *testing.T) {
	s, _ := newManagedTestStore(t)
	w := executionWriter(t, s)
	installation := uuid.NewString()
	changes := deploymentExecution(t, w)
	if err := changes.Claim(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.Initialize(t.Context(), installation, sandbox.Selection{DeploymentSpec: SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	nodes := deploymentService(t, s)
	token, err := EnrollmentTestToken(nodes.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 1}))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.NewString()
	if _, err := nodes.Enroll(t.Context(), token, deployment.Enrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: nodeID, Name: "Archive fixture", Provider: "docker", Credential: strings.Repeat("x", 64), BackendFingerprint: strings.Repeat("b", 64), CoreURL: s.placement.PublicURL()}); err != nil {
		t.Fatal(err)
	}
	onlineManagerNode(t, s, nodeID)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	result, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 1)
	if err != nil || result.State != "released" {
		t.Fatal(result, err)
	}
	listed, err := nodes.ListNodes(t.Context())
	if err != nil || len(listed) != 1 || listed[0].Active != 0 || listed[0].Retained != 0 || listed[0].Reserved != 0 {
		t.Fatal("unallocated archive retained placement capacity", listed, err)
	}
	if err := nodes.RemoveNode(t.Context(), nodeID); err != nil {
		t.Fatal("released placement prevented node removal", err)
	}
}

func TestManagedSessionArchiveOrdersConcurrentInput(t *testing.T) {
	s, w, _ := managedArchiveFixture(t)
	for range 8 {
		tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.ReserveEnvironmentInput(t.Context(), tenant, session.ID, "racing-input", []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"racing"}`)}})
			if err != nil && !errors.Is(err, sessions.ErrEnvironmentUnavailable) {
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
	s, w, _ := managerFixture(t, 1, 1)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	if _, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 0); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("archive accepted file-managed deployment", err)
	}
}
