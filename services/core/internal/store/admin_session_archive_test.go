package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func managedArchiveFixture(t *testing.T) (*Store, *Store, string) {
	t.Helper()
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{37}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionWriter(t, s)
	installation := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	if _, err := w.InitializeSandboxDeployment(t.Context(), installation, e2bSelection()); err != nil {
		t.Fatal(err)
	}
	return s, w, installation
}

func managedArchiveSession(t *testing.T, s *Store, input CreateSessionInput) (string, Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'admin-archive',$2)", tenant, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Archive fixture',$1,'service_account',$2)", tenant, "project:"+tenant); err != nil {
		t.Fatal(err)
	}
	return tenant, session
}

func archiveAllocation(t *testing.T, w *Store, tenant string, session Session, installation string) RuntimeAllocation {
	t.Helper()
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, installation, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestManagedSessionArchiveUnallocatedAndGuards(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	input := managerSessionInput(uuid.NewString())
	input.InitialInputs = []Input{{Kind: "message", Payload: json.RawMessage(`{"text":"waiting"}`)}}
	tenant, session := managedArchiveSession(t, s, input)
	ctx := adminDeleteContext(t.Context(), tenant, uuid.NewString())
	active, err := s.GetManagedSessionArchive(t.Context(), tenant, session.ID)
	if err != nil || active.State != "active" || active.SessionID != session.ID || active.EnvironmentID != session.Environment.ID {
		t.Fatal("unallocated Session status", active, err)
	}
	for _, generation := range []uint64{0, 2, ^uint64(0)} {
		if _, err := w.ArchiveManagedSession(ctx, tenant, session.ID, generation); !errors.Is(err, ErrSandboxDeploymentConflict) {
			t.Fatal("archive accepted wrong generation", generation, err)
		}
	}
	if _, err := s.ArchiveManagedSession(ctx, tenant, session.ID, 1); !errors.Is(err, ErrExecutionAuthority) {
		t.Fatal("unleased archive accepted", err)
	}
	for _, other := range []string{uuid.NewString(), "malformed"} {
		if _, err := w.ArchiveManagedSession(ctx, tenant, other, 1); !errors.Is(err, ErrNotFound) {
			t.Fatal("unknown archive", err)
		}
		if _, err := s.GetManagedSessionArchive(ctx, tenant, other); !errors.Is(err, ErrNotFound) {
			t.Fatal("unknown status", err)
		}
	}
	if _, err := w.ArchiveManagedSession(ctx, uuid.NewString(), session.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign archive", err)
	}
	if _, err := s.GetManagedSessionArchive(ctx, uuid.NewString(), session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign status", err)
	}
	result, err := w.ArchiveManagedSession(ctx, tenant, session.ID, 1)
	if err != nil || result.State != "released" {
		t.Fatal("unallocated archive", result, err)
	}
	row, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || row.Environment.Status != "expired" || row.EnvironmentFailure != nil || row.PendingInput || row.EnvironmentInputActivity != nil || row.LastTurn != nil {
		t.Fatal("archive fabricated failed execution", row, err)
	}
	var reservationState string
	if err := s.pool.QueryRow(t.Context(), "SELECT state FROM environment_input_reservations WHERE session_id=$1", session.ID).Scan(&reservationState); err != nil || reservationState != "cancelled" {
		t.Fatal("archive left pending initial input", reservationState, err)
	}
	before := adminMutationSnapshot(t, s, "sessions", "environments", "environment_input_reservations", "session_events")
	retry, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 1)
	if err != nil || retry != result || !reflect.DeepEqual(before, adminMutationSnapshot(t, s, "sessions", "environments", "environment_input_reservations", "session_events")) {
		t.Fatal("archive retry changed Session history", retry, err)
	}
	if status, err := s.GetManagedSessionArchive(ctx, tenant, session.ID); err != nil || status != result {
		t.Fatal("status differs from committed archive", status, err)
	}
	if _, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, installation, runtimedevice.HashCredential(uuid.NewString())); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("archived Environment allocated after archive", err)
	}
	if _, err := s.ReserveEnvironmentInput(t.Context(), tenant, session.ID, "later", []Input{{Kind: "message", Payload: json.RawMessage(`{"text":"later"}`)}}); !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatal("archived Environment accepted new input", err)
	}
	view, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || view.Resources.Pending != 0 || view.Resources.Allocations != 0 {
		t.Fatal("unallocated archive still blocks switching", view, err)
	}
}

func TestManagedSessionArchiveRetainsHistoryAndSettledResources(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	owner := archiveAllocation(t, w, tenant, session, installation)
	sourceFiles, sourceFileReader := testFiles(t, s.pool)
	file, err := sourceFiles.Create(t.Context(), files.CreateCommand{TenantID: tenant, Upload: uploadSource([]byte("retained source file"))})
	if err != nil {
		t.Fatal(err)
	}
	input := submitMessage(t, s, tenant, session.ID, "completed")
	transition(t, w, tenant, session.ID, input.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	if err := w.AppendTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 1, []ExecutionEvent{{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"retained"}`)}}); err != nil {
		t.Fatal(err)
	}
	body := []byte("retained artifact")
	if err := s.StageTurnArtifacts(t.Context(), tenant, session.ID, input.TurnID, session.Environment.ID, bytes.NewReader(artifactArchive(t, map[string][]byte{"outputs/result.txt": body}))); err != nil {
		t.Fatal(err)
	}
	transition(t, w, tenant, session.ID, input.TurnID, sessions.TurnInProgress, sessions.TurnCompleted)
	history := adminMutationSnapshot(t, s, "sessions", "turns", "session_items", "session_artifacts", "source_files", "pg_largeobject", "pg_largeobject_metadata")
	request := uuid.NewString()
	result, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, request), tenant, session.ID, 1)
	if err != nil || result.State != "cleanup_pending" {
		t.Fatal(result, err)
	}
	assertAdminMutationAudit(t, s, tenant, request, "archive", "session", session.ID)
	if !reflect.DeepEqual(history, adminMutationSnapshot(t, s, "sessions", "turns", "session_items", "session_artifacts", "source_files", "pg_largeobject", "pg_largeobject_metadata")) {
		t.Fatal("archive changed persisted history or artifacts")
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
		t.Fatal("archive retained runtime authority", err)
	}
	if _, err := w.ReleaseRuntimeAllocation(t.Context(), owner); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("archive discarded unknown Create ownership", err)
	}
	replay, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, installation, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil || !replay.Replayed || replay.ID != owner.ID || replay.DeviceID != owner.DeviceID || replay.State != "cleanup_pending" {
		t.Fatal("late provisioning retry replaced archived allocation", replay, err)
	}
	if _, err := w.RequestRuntimeCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || current.EnvironmentFailure != nil || current.LastTurn == nil || current.LastTurn.Status != sessions.TurnCompleted || current.Environment.Status != "expired" {
		t.Fatal("cleanup rewrote completed outcome", current, err)
	}
	if _, err := w.SettleRuntimeCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if result, err := s.GetManagedSessionArchive(t.Context(), tenant, session.ID); err != nil || result.State != "released" {
		t.Fatal("release not reflected", result, err)
	}
	page, err := s.ListSessionArtifacts(t.Context(), tenant, session.ID, "", "", 100, true)
	if err != nil || len(page.Artifacts) != 1 {
		t.Fatal(page, err)
	}
	if err := s.ReadSessionArtifact(t.Context(), tenant, session.ID, page.Artifacts[0].ID, func(_ SessionArtifact, r io.Reader) error {
		got, err := io.ReadAll(r)
		if !bytes.Equal(got, body) {
			t.Error("archive damaged published artifact bytes")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := sourceFileReader.Read(t.Context(), tenant, file.ID, func(_ files.File, r io.Reader) error {
		got, err := io.ReadAll(r)
		if string(got) != "retained source file" {
			t.Error("archive damaged source file bytes")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSessionArchiveAuditFailureRollsBack(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	archiveAllocation(t, w, tenant, session, installation)
	input := submitMessage(t, s, tenant, session.ID, "running")
	transition(t, w, tenant, session.ID, input.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	rejectAdminAuditInsert(t, s)
	tables := []string{"sessions", "environments", "turns", "session_events", "devices", "runtime_allocations", "runtime_placements", "environment_input_reservations", "admin_audit_log"}
	before := adminMutationSnapshot(t, s, tables...)
	count := adminAuditRejections(t, s)
	_, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, rejectedAdminRequest), tenant, session.ID, 1)
	requireAdminAuditFailure(t, s, err, count)
	if !reflect.DeepEqual(before, adminMutationSnapshot(t, s, tables...)) {
		t.Fatal("failed audit retained archive, revocation or cancellation")
	}
	if _, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	turn, err := s.GetTurn(t.Context(), tenant, session.ID, input.TurnID)
	if err != nil || turn.Status != sessions.TurnInProgress || turn.CancelRequestedAt.IsZero() {
		t.Fatal("archive did not request cancellation or fabricated settlement", turn, err)
	}
}

func TestManagedSessionArchivePreservesFailuresAndRejectsSelfHosted(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	owner := archiveAllocation(t, w, tenant, session, installation)
	if _, err := s.pool.Exec(t.Context(), "UPDATE environments SET initialization='running' WHERE id=$1", owner.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if err := w.FailEnvironmentInitialization(t.Context(), EnvironmentInitialization{EnvironmentID: owner.EnvironmentID, SessionID: owner.SessionID, TenantID: owner.TenantID, DeviceID: owner.DeviceID}, ProvisioningFailure{Step: ProvisioningSetupCommand, Index: 0, ExitCode: 2}); err != nil {
		t.Fatal(err)
	}
	failed, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || failed.EnvironmentFailure == nil {
		t.Fatal("failure fixture", err)
	}
	otherTenant, selfHosted := managedArchiveSession(t, s, environmentInput(uuid.NewString(), "self_hosted", "/workspace"))
	if _, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(after.EnvironmentFailure, failed.EnvironmentFailure) || after.Environment.Status != "failed" {
		t.Fatal("archive changed recorded provisioning failure", after, err)
	}
	before := adminMutationSnapshot(t, s, "sessions", "environments", "admin_audit_log")
	if _, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), otherTenant, uuid.NewString()), otherTenant, selfHosted.ID, 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("self-hosted archive accepted", err)
	}
	if _, err := s.GetManagedSessionArchive(t.Context(), otherTenant, selfHosted.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("self-hosted cleanup projected", err)
	}
	if !reflect.DeepEqual(before, adminMutationSnapshot(t, s, "sessions", "environments", "admin_audit_log")) {
		t.Fatal("self-hosted archive changed resources")
	}
}
