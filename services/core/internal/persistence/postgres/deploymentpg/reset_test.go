package deploymentpg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// resetAudit lists the audited deployment actions in order, each with the
// administrator credential that recorded it.
func resetAudit(t *testing.T, f fixture) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT action || ' ' || admin_credential_id FROM admin_audit_log WHERE resource_type='sandbox_deployment' ORDER BY created_at, id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries []string
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func resetCount(t *testing.T, f fixture, query string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(t.Context(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A reset keeps its original request time and deadline across a replay and an
// escalation, and completion clears the deployment under the starter's audit
// source.
func TestResetStartEscalateAndComplete(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	if _, err := f.service.CreateEnrollment(admin(t), deployment.Capacity{MaxActive: 1, MaxRetained: 1}); err != nil {
		t.Fatal(err)
	}

	auto := deployment.ResetRequest{ExpectedGeneration: view.Generation, Clear: deployment.ResetAuto}
	if err := changes.StartReset(admin(t), installation, auto); err != nil {
		t.Fatal(err)
	}
	started, err := f.service.View(t.Context())
	if err != nil || started.Reset == nil || started.Reset.DeadlineAt == nil || started.Reset.DeadlineAt.Sub(started.Reset.RequestedAt) != time.Hour || started.Reset.ForcedAt != nil {
		t.Fatalf("started reset = %+v, %v", started.Reset, err)
	}
	if err := changes.StartReset(admin(t), installation, auto); err != nil {
		t.Fatal(err)
	}
	force := auto
	force.Clear = deployment.ResetForce
	if err := changes.StartReset(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "escalating-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()}), installation, force); err != nil {
		t.Fatal(err)
	}
	forced, err := f.service.View(t.Context())
	if err != nil || forced.Reset == nil || forced.Reset.Clear != deployment.ResetForce || forced.Reset.ForcedAt == nil ||
		!forced.Reset.RequestedAt.Equal(started.Reset.RequestedAt) || !forced.Reset.DeadlineAt.Equal(*started.Reset.DeadlineAt) {
		t.Fatalf("forced reset = %+v, %v", forced.Reset, err)
	}

	// Completion carries no caller source: it audits as the administrator who
	// started the reset.
	committed, err := changes.CompleteReset(t.Context(), installation, view.Generation, forced.Reset.RequestedAt)
	if err != nil || committed != view.Generation+1 {
		t.Fatalf("CompleteReset = %d, %v", committed, err)
	}
	cleared, err := f.service.View(t.Context())
	if err != nil || cleared.Generation != committed || cleared.Provider != "" || cleared.Reset != nil || cleared.OwnerEpoch != view.OwnerEpoch+1 {
		t.Fatalf("cleared deployment = %+v, %v", cleared, err)
	}
	if nodes := resetCount(t, f, "SELECT count(*) FROM runtime_nodes WHERE removed_at IS NULL"); nodes != 0 {
		t.Fatalf("%d nodes survived the reset", nodes)
	}
	if tokens := resetCount(t, f, "SELECT count(*) FROM runtime_node_enrollments WHERE consumed_at IS NULL AND expires_at > clock_timestamp()"); tokens != 0 {
		t.Fatalf("%d enrollment tokens survived the reset", tokens)
	}
	if got, want := resetAudit(t, f), []string{"reset_start fixture-admin", "reset_force escalating-admin", "reset_complete fixture-admin"}; !slices.Equal(got, want) {
		t.Fatalf("audit = %q, want %q", got, want)
	}
}

// A passed auto deadline escalates under the starter's source, and a
// cancellation restores admission.
func TestResetDeadlineAndCancel(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	if err := changes.StartReset(admin(t), installation, deployment.ResetRequest{ExpectedGeneration: view.Generation, Clear: deployment.ResetAuto}); err != nil {
		t.Fatal(err)
	}
	if err := changes.AdvanceResetDeadline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE runtime_deployment SET reset_deadline_at = clock_timestamp() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if err := changes.AdvanceResetDeadline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if forced, err := f.service.View(t.Context()); err != nil || forced.Reset == nil || forced.Reset.Clear != deployment.ResetForce {
		t.Fatalf("reset after its deadline = %+v, %v", forced.Reset, err)
	}
	if err := changes.CancelReset(admin(t), installation, view.Generation); err != nil {
		t.Fatal(err)
	}
	if paused := resetCount(t, f, "SELECT count(*) FROM runtime_deployment WHERE admission_paused OR reset_clear IS NOT NULL OR reset_audit IS NOT NULL"); paused != 0 {
		t.Fatal("cancellation left the reset or paused admission")
	}
	if got, want := resetAudit(t, f), []string{"reset_start fixture-admin", "reset_deadline fixture-admin", "reset_cancel fixture-admin"}; !slices.Equal(got, want) {
		t.Fatalf("audit = %q, want %q", got, want)
	}
}

// A rejected audit entry and a lost lease leave no reset behind.
func TestResetWritesNothingWithoutAuditOrLease(t *testing.T) {
	f := newFixture(t)
	closed := setupClosedExecution(t, f)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	request := deployment.ResetRequest{ExpectedGeneration: view.Generation, Clear: deployment.ResetAuto}
	if err := changes.StartReset(adminaudit.WithSource(t.Context(), adminaudit.Source{}), installation, request); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatalf("StartReset with an invalid source = %v", err)
	}
	if err := closed.StartReset(admin(t), installation, request); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatalf("StartReset on a closed lease = %v", err)
	}
	if paused := resetCount(t, f, "SELECT count(*) FROM runtime_deployment WHERE admission_paused OR reset_clear IS NOT NULL"); paused != 0 {
		t.Fatal("a rejected reset paused admission")
	}
}

func TestResetSessionsAndAddressBindings(t *testing.T) {
	f := newFixture(t)
	if _, err := f.adapter.ResetSessions(t.Context(), "not-a-session", false); !errors.Is(err, deployment.ErrInvalidInput) {
		t.Fatalf("ResetSessions with a malformed cursor = %v", err)
	}
	if sessions, err := f.adapter.ResetSessions(t.Context(), uuid.NewString(), true); err != nil || len(sessions) != 0 {
		t.Fatalf("ResetSessions without Sessions = %v, %v", sessions, err)
	}

	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	if bindings, err := f.service.AddressBindings(t.Context()); err != nil || bindings != (deployment.AddressBindings{Nodes: 1}) {
		t.Fatalf("AddressBindings = %+v, %v", bindings, err)
	}
	if bindings, err := f.adapter.AddressBindings(t.Context(), "https://other.example"); err != nil || bindings != (deployment.AddressBindings{Nodes: 1, NodesOnOtherAddress: 1}) {
		t.Fatalf("AddressBindings for another address = %+v, %v", bindings, err)
	}
}
