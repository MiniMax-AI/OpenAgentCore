package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func SandboxResetTestContext(ctx context.Context) context.Context {
	return adminaudit.WithSource(ctx, adminaudit.Source{CredentialID: "reset-fixture", ActorLabel: "operator", RequestID: "reset-request", TraceID: "reset-trace"})
}
func resetAndSelect(t *testing.T, w *Store, installation string, generation uint64, input sandbox.Selection) (deployment.View, error) {
	t.Helper()
	ctx := SandboxResetTestContext(t.Context())
	if err := deploymentExecution(t, w).StartReset(ctx, installation, deployment.ResetRequest{Clear: "auto", ExpectedGeneration: generation}); err != nil {
		return deployment.View{}, err
	}
	reset, err := deploymentService(t, w).View(ctx)
	if err != nil {
		return deployment.View{}, err
	}
	empty, err := deploymentExecution(t, w).CompleteReset(ctx, installation, generation, reset.Reset.RequestedAt)
	if err != nil {
		return deployment.View{}, err
	}
	input.ExpectedGeneration = empty
	return deploymentExecution(t, w).Initialize(ctx, installation, input)
}

func assertResetPartition(t *testing.T, view deployment.View) {
	t.Helper()
	if view.Reset == nil {
		t.Fatal("missing reset")
	}
	remaining := view.Reset.Remaining
	if remaining.Busy+remaining.Idle+remaining.Cleanup != view.Resources.Allocations+view.Resources.Pending {
		t.Fatalf("inconsistent reset partition: %+v", view)
	}
	var offline int64
	for _, node := range remaining.OfflineNodes {
		offline += node.Resources
	}
	if offline != remaining.OnOfflineNodes || offline > view.Resources.Allocations+view.Resources.Pending {
		t.Fatalf("inconsistent offline subset: %+v", remaining)
	}
}

// startReset starts or escalates the reset and returns the deployment view
// read after it commits.
func startReset(t *testing.T, ctx context.Context, w *Store, installation string, input deployment.ResetRequest) (deployment.View, error) {
	t.Helper()
	if err := deploymentExecution(t, w).StartReset(ctx, installation, input); err != nil {
		return deployment.View{}, err
	}
	return deploymentService(t, w).View(ctx)
}

func TestSandboxResetAutoUsesStartedWorkAndLockedRecheck(t *testing.T) {
	for _, kind := range []string{"idle", "queued", "in_progress", "waiting", "subagent_queued", "subagent_in_progress", "subagent_waiting", "file_write", "suspended", "pending"} {
		t.Run(kind, func(t *testing.T) {
			s, w, installation := managedArchiveFixture(t)
			tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
			var owner deployment.Allocation
			if kind != "pending" {
				owner = archiveAllocation(t, w, tenant, session, installation)
			}
			busy := kind == "in_progress" || kind == "waiting" || kind == "subagent_in_progress" || kind == "subagent_waiting" || kind == "file_write"
			switch kind {
			case "queued", "in_progress", "waiting":
				runtimeSuspensionSQL(t, s.pool, `INSERT INTO turns(id,session_id,status) VALUES($1,$2,$3)`, uuid.NewString(), session.ID, kind)
			case "subagent_queued", "subagent_in_progress", "subagent_waiting":
				parent, child := uuid.NewString(), uuid.NewString()
				runtimeSuspensionSQL(t, s.pool, `INSERT INTO turns(id,session_id,status,completed_at) VALUES($1,$2,'completed',clock_timestamp())`, parent, session.ID)
				runtimeSuspensionSQL(t, s.pool, `INSERT INTO turn_events(session_id,turn_id,ordinal,kind,payload) VALUES($1,$2,1,'subagent','{}')`, session.ID, parent)
				runtimeSuspensionSQL(t, s.pool, `INSERT INTO subagent_identities(id,session_id,device_id,engine,native_id,parent_native_id,native_created_at,first_turn_id,first_event_ordinal) VALUES($1,$2,$3,'codex','child','root',1,$4,1)`, child, session.ID, owner.DeviceID, parent)
				runtimeSuspensionSQL(t, s.pool, `INSERT INTO subagent_turns(id,session_id,subagent_id,native_id,status,created_at) VALUES($1,$2,$3,'child-turn',$4,clock_timestamp())`, uuid.NewString(), session.ID, child, strings.TrimPrefix(kind, "subagent_"))
			case "file_write":
				runtimeSuspensionSQL(t, s.pool, `INSERT INTO environment_file_writes(id,environment_id,device_id,request_sha256) VALUES($1,$2,$3,$4)`, uuid.NewString(), session.Environment.ID, owner.DeviceID, strings.Repeat("a", 64))
			case "suspended":
				runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_allocations SET compute_phase='suspended', compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID)
			}
			reset, err := startReset(t, SandboxResetTestContext(t.Context()), w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
			if err != nil {
				t.Fatal(err)
			}
			assertResetPartition(t, reset)
			assertSandboxSnapshotEquivalent(t, s.pool)
			if (reset.Reset.Remaining.Busy == 1) != busy {
				t.Fatalf("wrong busy classification: %+v", reset.Reset.Remaining)
			}
			page, err := deploymentStore(w).ResetSessions(t.Context(), "", false)
			if err != nil || (len(page) == 0) != busy {
				t.Fatal("auto eligibility", page, err)
			}
			_, err = w.ArchiveSandboxResetSession(t.Context(), tenant, session.ID, 1, reset.Reset.RequestedAt)
			if busy {
				if !errors.Is(err, ErrSandboxResetSessionBusy) {
					t.Fatal("auto cut active work", err)
				}
				if err := deploymentExecution(t, w).StartReset(SandboxResetTestContext(t.Context()), installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "force"}); err != nil {
					t.Fatal(err)
				}
				_, err = w.ArchiveSandboxResetSession(t.Context(), tenant, session.ID, 1, reset.Reset.RequestedAt)
			}
			if err != nil {
				t.Fatal("archive", err)
			}
			archived, err := s.GetSession(t.Context(), tenant, session.ID)
			if err != nil || archived.Environment.Status != "expired" {
				t.Fatal("archive not durable", archived, err)
			}
			var auditTenant, auditProject, credential, request string
			if err := s.pool.QueryRow(t.Context(), `SELECT tenant_id::text,project_id::text,admin_credential_id,request_id FROM admin_audit_log WHERE action='archive' AND resource_id=$1`, session.ID).Scan(&auditTenant, &auditProject, &credential, &request); err != nil || auditTenant != tenant || auditProject != tenant || credential != "reset-fixture" || request != "reset-request" {
				t.Fatal("background provenance was not reconstructed", err)
			}
		})
	}
}

func TestSandboxResetCancellationABADeadlineAndGeneration(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	ctx := SandboxResetTestContext(t.Context())
	first, err := startReset(t, ctx, w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reset.DeadlineAt == nil || first.Reset.DeadlineAt.Sub(first.Reset.RequestedAt) < 3599*time.Second {
		t.Fatal("default deadline", first)
	}
	shorter := int32(300)
	replay, err := startReset(t, ctx, w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto", DeadlineSeconds: &shorter})
	if err != nil || !replay.Reset.RequestedAt.Equal(first.Reset.RequestedAt) || !replay.Reset.DeadlineAt.Equal(*first.Reset.DeadlineAt) {
		t.Fatal("retry moved durable deadline", replay, err)
	}
	if err = deploymentExecution(t, w).CancelReset(ctx, installation, 1); err != nil {
		t.Fatal(err)
	}
	second, err := startReset(t, ctx, w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.ArchiveSandboxResetSession(t.Context(), tenant, session.ID, 1, first.Reset.RequestedAt); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("cancelled reset archived successor work", err)
	}
	runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_deployment SET reset_deadline_at=clock_timestamp()-interval '1 second'`)
	if err := deploymentExecution(t, w).AdvanceResetDeadline(t.Context()); err != nil {
		t.Fatal(err)
	}
	forced, err := deploymentService(t, s).View(t.Context())
	if err != nil || forced.Reset.Clear != "force" || forced.Reset.ForcedAt == nil || !forced.Reset.RequestedAt.Equal(second.Reset.RequestedAt) {
		t.Fatal("deadline not durable", forced, err)
	}
	if err := deploymentExecution(t, w).StartReset(ctx, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"}); !errors.Is(err, deployment.ErrResetInProgress) {
		t.Fatal("force downgraded", err)
	}
	if err := deploymentExecution(t, w).StartReset(ctx, installation, deployment.ResetRequest{ExpectedGeneration: 0, Clear: "force"}); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("stale reset precedence", err)
	}
	if _, err := w.ArchiveSandboxResetSession(t.Context(), tenant, session.ID, 1, second.Reset.RequestedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).CompleteReset(ctx, installation, 1, first.Reset.RequestedAt); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("cancelled reset finalized successor", err)
	}
	committed, err := deploymentExecution(t, w).CompleteReset(ctx, installation, 1, second.Reset.RequestedAt)
	if err != nil || committed != 2 {
		t.Fatal("reset commit", committed, err)
	}
	empty, err := deploymentService(t, s).View(t.Context())
	if err != nil || empty.Provider != "" || empty.Generation != 2 || empty.Reset != nil || empty.InstallationID != installation || empty.Configuration != nil || empty.Specification != nil {
		t.Fatal("reset commit", empty, err)
	}
	if err := deploymentExecution(t, w).CancelReset(ctx, installation, 1); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("stale cancel", err)
	}
	if err := deploymentExecution(t, w).CancelReset(ctx, installation, 2); err != nil {
		t.Fatal("cancel without reset", err)
	}
}

func TestSandboxResetAutoRechecksTurnStartedAfterListing(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	archiveAllocation(t, w, tenant, session, installation)
	reset, err := startReset(t, SandboxResetTestContext(t.Context()), w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := deploymentStore(w).ResetSessions(t.Context(), "", false)
	if err != nil || len(page) != 1 {
		t.Fatal(page, err)
	}
	// Existing live input remains admitted; Turn transition takes the same
	// Session lock that the later conditional archive must reacquire.
	turn := submitMessage(t, s, tenant, session.ID, "after-list")
	transition(t, w, tenant, session.ID, turn.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	if _, err := w.ArchiveSandboxResetSession(t.Context(), tenant, session.ID, 1, reset.Reset.RequestedAt); !errors.Is(err, ErrSandboxResetSessionBusy) {
		t.Fatal("listed idle candidate cut a new Turn", err)
	}
	before := adminMutationSnapshot(t, s, "sessions", "environments", "environment_input_reservations", "turns", "runtime_placements")
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); !errors.Is(err, placement.ErrResetAdmission) {
		t.Fatal("new hosted admission during reset", err)
	}
	after := adminMutationSnapshot(t, s, "sessions", "environments", "environment_input_reservations", "turns", "runtime_placements")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected admission left provisional rows")
	}
	if err := deploymentExecution(t, w).CancelReset(SandboxResetTestContext(t.Context()), installation, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(t.Context(), tenant, managerSessionInput(uuid.NewString())); err != nil {
		t.Fatal("cancel did not restore admission", err)
	}
}

func TestSandboxResetAuditFailureRollsBackPauseAndCompletion(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	rejectAdminAuditInsert(t, s)
	rejected := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "reset-fixture", ActorLabel: "operator", RequestID: rejectedAdminRequest, TraceID: "reset-trace"})
	if err := deploymentExecution(t, w).StartReset(rejected, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "force"}); err == nil {
		t.Fatal("reset committed without audit")
	}
	view, err := deploymentService(t, s).View(t.Context())
	if err != nil || view.Reset != nil || view.Generation != 1 {
		t.Fatal("failed audit left reset state", view, err)
	}
	reset, err := startReset(t, SandboxResetTestContext(t.Context()), w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "force"})
	if err != nil {
		t.Fatal(err)
	}
	runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_deployment SET reset_audit=jsonb_set(reset_audit,'{RequestID}',to_jsonb($1::text))`, rejectedAdminRequest)
	if _, err := deploymentExecution(t, w).CompleteReset(t.Context(), installation, 1, reset.Reset.RequestedAt); err == nil {
		t.Fatal("completion committed without audit")
	}
	view, err = deploymentService(t, s).View(t.Context())
	if err != nil || view.Reset == nil || view.Provider != "e2b" || view.Generation != 1 || view.OwnerEpoch != reset.OwnerEpoch {
		t.Fatal("failed completion destroyed committed provider", view, err)
	}
}

func TestSandboxResetSnapshotCountsOfflineOwnershipOnce(t *testing.T) {
	s, w, process := managerFixture(t, 10, 10)
	// Reuse the real placement fixture, then adopt its selection as Web-managed.
	runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_deployment SET web_managed=true,local_node_id=NULL`)
	runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_nodes SET deployment_generation=1,specification_digest=$1`, SandboxDeploymentTestSpec("docker").Digest("docker"))
	_, pending := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	tenant, suspended := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	allocation := archiveAllocation(t, w, tenant, suspended, process.InstallationID)
	runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_allocations SET compute_phase='suspended',compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, allocation.ID)
	tenant, deleted := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	archiveAllocation(t, w, tenant, deleted, process.InstallationID)
	if err := s.DeleteSession(t.Context(), tenant, deleted.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := startReset(t, SandboxResetTestContext(t.Context()), w, process.InstallationID, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	assertResetPartition(t, reset)
	if reset.Resources != (deployment.Resources{Allocations: 2, Pending: 1}) || reset.Reset.Remaining.Idle != 2 || reset.Reset.Remaining.Cleanup != 1 {
		t.Fatal("duplicated placement or missing deleted receipt", reset)
	}
	for _, state := range []string{"preparing", "stale", "epoch", "disconnected"} {
		runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_nodes SET connected_epoch=(SELECT owner_epoch FROM runtime_deployment)`)
		onlineManagerNode(t, s, process.LocalNodeID)
		switch state {
		case "preparing":
			runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_nodes SET provider_ready=false`)
		case "stale":
			runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_nodes SET last_seen_at=clock_timestamp()-interval '46 seconds'`)
		case "epoch":
			runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_nodes SET connected_epoch=connected_epoch+1`)
		case "disconnected":
			runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_nodes SET connection_id=NULL`)
		}
		view, err := deploymentService(t, s).View(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertResetPartition(t, view)
		assertSandboxSnapshotEquivalent(t, s.pool)
		want := int64(3)
		if state == "preparing" {
			want = 0
		}
		if view.Reset.Remaining.OnOfflineNodes != want {
			t.Fatalf("%s presence: %+v", state, view.Reset.Remaining)
		}
		if want > 0 && (len(view.Reset.Remaining.OfflineNodes) != 1 || view.Reset.Remaining.OfflineNodes[0].NodeID != process.LocalNodeID || view.Reset.Remaining.OfflineNodes[0].Resources != 3) {
			t.Fatal("offline ownership projection", view.Reset.Remaining)
		}
	}
	if _, err := deploymentExecution(t, w).CompleteReset(t.Context(), process.InstallationID, 1, reset.Reset.RequestedAt); err == nil {
		t.Fatal("offline resources were treated as cleaned")
	}
	if err := deploymentService(t, s).RemoveNode(t.Context(), process.LocalNodeID); !errors.Is(err, deployment.ErrNodeInUse) {
		t.Fatal("removed node with reset resources", err)
	}
	if row, err := sessionAdapter(s).GetEnvironment(t.Context(), pending.TenantID, pending.Environment.ID); err == nil && row.Status == "expired" {
		t.Fatal("projection mutated pending resource")
	}
}

func TestSandboxResetPaginationSkipsBusyPrefixAndPreservesSelfHosted(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	// Use deterministic ordered UUIDs so more than one page of busy rows precedes
	// idle work. Listing must not repeatedly return the busy prefix.
	for i := 1; i <= 35; i++ {
		tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		// IDs are immutable API identities, so seed this bounded scheduling fixture
		// directly instead of modifying an existing Session primary key.
		runtimeSuspensionSQL(t, s.pool, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration) SELECT $1::uuid,tenant_id,engine,$1::text,$1::text,configuration FROM sessions WHERE id=$2`, id, session.ID)
		runtimeSuspensionSQL(t, s.pool, `INSERT INTO environments(id,session_id) VALUES($1,$2)`, uuid.NewString(), id)
		if i <= 34 {
			runtimeSuspensionSQL(t, s.pool, `INSERT INTO turns(id,session_id,status) VALUES($1,$2,'in_progress')`, uuid.NewString(), id)
		}
		if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
			t.Fatal(err)
		}
	}
	selfTenant, self := managedArchiveSession(t, s, environmentInput(uuid.NewString(), "self_hosted", "/workspace"))
	reset, err := startReset(t, SandboxResetTestContext(t.Context()), w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := deploymentStore(w).ResetSessions(t.Context(), "", false)
	if err != nil || len(page) != 1 || page[0].SessionID != "00000000-0000-4000-8000-000000000035" {
		t.Fatal("busy prefix starved idle work", page, err)
	}
	if _, err := w.ArchiveSandboxResetSession(t.Context(), page[0].TenantID, page[0].SessionID, 1, reset.Reset.RequestedAt); err != nil {
		t.Fatal(err)
	}
	if err := deploymentExecution(t, w).StartReset(SandboxResetTestContext(t.Context()), installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "force"}); err != nil {
		t.Fatal(err)
	}
	first, err := deploymentStore(w).ResetSessions(t.Context(), "", true)
	if err != nil || len(first) != 32 {
		t.Fatal(first, err)
	}
	second, err := deploymentStore(w).ResetSessions(t.Context(), first[31].SessionID, true)
	if err != nil || len(second) != 2 {
		t.Fatal(second, err)
	}
	if _, err := w.ArchiveSandboxResetSession(t.Context(), selfTenant, self.ID, 1, reset.Reset.RequestedAt); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("self-hosted reset archive", err)
	}
	view, err := s.GetSession(t.Context(), selfTenant, self.ID)
	if err != nil || view.Environment.Status == "expired" {
		t.Fatal("reset changed self-hosted Session", view, err)
	}
}

func TestSandboxResetOwnerRestartRetainsDeadlineAndProvenance(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	reset, err := startReset(t, SandboxResetTestContext(t.Context()), w, installation, deployment.ResetRequest{ExpectedGeneration: 1, Clear: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	// Model an abrupt owner loss and wait for PostgreSQL to terminate that backend,
	// rather than assuming local TCP cleanup acknowledges advisory-lock release.
	var stopped bool
	if err := s.pool.QueryRow(t.Context(), `SELECT pg_terminate_backend($1,1000)`, executionOwnerPID(t, s.pool)).Scan(&stopped); err != nil || !stopped {
		t.Fatal(stopped, err)
	}
	successor := executionWriter(t, s)
	current, err := deploymentService(t, s).View(t.Context())
	if err != nil || !current.Reset.RequestedAt.Equal(reset.Reset.RequestedAt) || !current.Reset.DeadlineAt.Equal(*reset.Reset.DeadlineAt) {
		t.Fatal("restart moved reset deadline", current, err)
	}
	if err := deploymentExecution(t, w).CancelReset(SandboxResetTestContext(t.Context()), installation, 1); err == nil {
		t.Fatal("detached writer cancelled successor reset")
	}
	runtimeSuspensionSQL(t, s.pool, `UPDATE runtime_deployment SET reset_deadline_at=clock_timestamp()-interval '1 second'`)
	if err := deploymentExecution(t, successor).AdvanceResetDeadline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := successor.ArchiveSandboxResetSession(t.Context(), tenant, session.ID, 1, reset.Reset.RequestedAt); err != nil {
		t.Fatal(err)
	}
	committed, err := deploymentExecution(t, successor).CompleteReset(t.Context(), installation, 1, reset.Reset.RequestedAt)
	if err != nil || committed != 2 {
		t.Fatal(committed, err)
	}
	empty, err := deploymentService(t, s).View(t.Context())
	if err != nil || empty.Generation != 2 || empty.Provider != "" {
		t.Fatal(empty, err)
	}
	var actions []string
	if err := s.pool.QueryRow(t.Context(), `SELECT array_agg(action ORDER BY action) FROM admin_audit_log WHERE action IN ('reset_start','reset_deadline','reset_complete') AND request_id='reset-request' AND admin_credential_id='reset-fixture'`).Scan(&actions); err != nil || len(actions) != 3 {
		t.Fatal("restart lost requester audit", actions, err)
	}
}
