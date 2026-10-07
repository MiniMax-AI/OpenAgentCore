package integration

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// An installation never adopts hosted work admitted before it claimed the
// deployment: a pending Session or an unreleased allocation refuses the claim.
func TestRuntimeDeploymentClaimAdoptsNoUnclaimedWork(t *testing.T) {
	s, _ := newManagedTestStore(t)
	changes := deploymentExecution(t, executionWriter(t, s))
	installation, tenant := uuid.NewString(), uuid.NewString()
	pending, _ := localEnvironment(t, s, tenant)
	if err := changes.Claim(t.Context(), installation); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("pending Session adopted", err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: pending.ID}); err != nil {
		t.Fatal(err)
	}
	session, environment := localEnvironment(t, s, tenant)
	owner, err := changes.ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, installation, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err := changes.Claim(t.Context(), installation); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("unclaimed allocation adopted", err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.RequestCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.ReleaseAllocation(t.Context(), owner); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("unknown creation lost cleanup ownership", err)
	}
	if err := changes.Claim(t.Context(), installation); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("deleted unknown allocation did not block adoption", err)
	}
	if _, err := changes.SettleCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.ReleaseAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err := changes.Claim(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	if err := changes.RequireUnclaimed(t.Context()); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("owner without runtimes accepted a claimed deployment", err)
	}
}

func TestRuntimeDeploymentResetPreservesCreationRetriesAndOtherPlacements(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant := uuid.NewString()
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted"}}`)}
	existing, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	ctx := SandboxResetTestContext(t.Context())
	if _, err := startReset(t, ctx, w, installation, deployment.ResetRequest{Clear: "auto", ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || replay.ID != existing.ID {
		t.Fatal("creation retry lost identity", err)
	}
	input.IdempotencyKey = uuid.NewString()
	if _, err := s.CreateSession(t.Context(), tenant, input); !errors.Is(err, placement.ErrResetAdmission) {
		t.Fatal("reset created hosted Session", err)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejection left partial Session", count, err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: existing.Environment.ID}, installation, runtimedevice.HashCredential(uuid.NewString())); !errors.Is(err, placement.ErrResetAdmission) {
		t.Fatal("reset reserved new allocation", err)
	}
	for _, kind := range []string{"none", "self_hosted"} {
		input.IdempotencyKey = uuid.NewString()
		input.Configuration = json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"` + kind + `"}}`)
		if _, err := s.CreateSession(t.Context(), tenant, input); err != nil {
			t.Fatal("reset blocked unrelated placement", kind, err)
		}
	}
	if err := deploymentExecution(t, w).CancelReset(ctx, installation, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: existing.Environment.ID}, uuid.NewString(), runtimedevice.HashCredential(uuid.NewString())); !errors.Is(err, placement.ErrAdmissionClosed) {
		t.Fatal("wrong installation reserved resource", err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: existing.Environment.ID}, installation, runtimedevice.HashCredential(uuid.NewString())); err != nil {
		t.Fatal("cancelled reset did not reopen allocation", err)
	}
}
