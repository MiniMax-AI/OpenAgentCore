package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
)

func runtimeEnrollmentFixture(t *testing.T, s *Store, p identity.Principal) (Session, Environment, IssuedExecutorCredential) {
	t.Helper()
	input := environmentInput(uuid.NewString(), "self_hosted", "/workspace")
	input.Creator = p.Subject()
	session, err := s.CreateSession(t.Context(), p.TenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), p.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.IssueExecutorCredential(t.Context(), p, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	return session, environment, key
}

func TestRuntimeEnrollmentAuthorityAndRotation(t *testing.T) {
	s, _ := testStore(t)
	p := FixtureExecutorPrincipal(t, s, uuid.NewString())
	session, environment, key := runtimeEnrollmentFixture(t, s, p)
	ctx := t.Context()
	for _, other := range []identity.Principal{
		FixtureExecutorPrincipal(t, s, uuid.NewString()),
		{ProjectScope: p.ProjectScope, SubjectKind: p.SubjectKind, SubjectID: "other-user"},
		p,
	} {
		_, target, _ := runtimeEnrollmentFixture(t, s, other)
		if _, err := s.EnrollRuntime(ctx, target.ID, executorDigest(key.Token)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign target enrollment: %v", err)
		}
	}
	bound, err := s.EnrollRuntime(ctx, environment.ID, executorDigest(key.Token))
	if err != nil || bound.SessionID != session.ID || bound.EnvironmentID != environment.ID || bound.WorkspaceDirectory != "/workspace" {
		t.Fatalf("enrollment: %+v %v", bound, err)
	}
	if again, err := s.EnrollRuntime(ctx, environment.ID, executorDigest(key.Token)); err != nil || again != bound {
		t.Fatalf("retry changed binding: %+v %v", again, err)
	}
	if devices, err := s.ListExecutionDevices(ctx, p.TenantID); err != nil || len(devices) != 0 {
		t.Fatalf("enrolled Runtime entered general selection: %v", err)
	}
	auth := gateway.NewAuthenticator(s)
	if _, err := auth.AuthenticateBearer(ctx, bound.DeviceID, key.Token); err != nil {
		t.Fatal(err)
	}
	otherKey, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnrollRuntime(ctx, environment.ID, executorDigest(otherKey.Token)); !errors.Is(err, ErrDeviceBindingConflict) {
		t.Fatalf("another key replaced binding: %v", err)
	}
	rotated, err := s.RotateExecutorCredential(ctx, p, key.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AuthenticateBearer(ctx, bound.DeviceID, key.Token); !errors.Is(err, gateway.ErrAuthBadCredential) {
		t.Fatalf("old key after rotation: %v", err)
	}
	if _, err := auth.AuthenticateBearer(ctx, bound.DeviceID, rotated.Token); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		token  string
		denied bool
	}{{key.Token, true}, {rotated.Token, false}} {
		status, err := s.TouchAgentDaemonHeartbeat(ctx, device.Heartbeat{RuntimeID: bound.DeviceID, CredentialHash: executorDigest(check.token)})
		if err != nil || status.Deleted != check.denied {
			t.Fatalf("rotation heartbeat: %+v %v", status, err)
		}
	}
	if again, err := s.EnrollRuntime(ctx, environment.ID, executorDigest(rotated.Token)); err != nil || again != bound {
		t.Fatalf("rotation replaced identity: %+v %v", again, err)
	}
	if err := s.RevokeExecutorCredential(ctx, p, key.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetDeviceCredential(ctx, bound.DeviceID); err != nil || ok {
		t.Fatalf("revoked key authenticates: %v", err)
	}
	if _, err := s.GetSessionDevice(ctx, p.TenantID, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked binding dispatchable: %v", err)
	}
}

func TestRuntimeEnrollmentConcurrentAndDeletion(t *testing.T) {
	s, pool := testStore(t)
	p := FixtureExecutorPrincipal(t, s, uuid.NewString())
	session, environment, key := runtimeEnrollmentFixture(t, s, p)
	var wg sync.WaitGroup
	results := make(chan RuntimeEnrollment, 6)
	failures := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.EnrollRuntime(t.Context(), environment.ID, executorDigest(key.Token))
			results <- v
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var bound RuntimeEnrollment
	for got := range results {
		if bound.DeviceID == "" {
			bound = got
		}
		if got != bound {
			t.Fatal("concurrent enrollment created multiple identities")
		}
	}
	var allocations int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE environment_id=$1", environment.ID).Scan(&allocations); err != nil || allocations != 0 {
		t.Fatalf("enrollment allocated compute: %d %v", allocations, err)
	}
	if err := s.DeleteSession(t.Context(), p.TenantID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnrollRuntime(t.Context(), environment.ID, executorDigest(key.Token)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted enrollment: %v", err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), bound.DeviceID); err != nil || ok {
		t.Fatalf("deleted Session authenticates: %v", err)
	}
	status, err := s.TouchAgentDaemonHeartbeat(t.Context(), device.Heartbeat{RuntimeID: bound.DeviceID, CredentialHash: executorDigest(key.Token)})
	if err != nil || !status.Deleted {
		t.Fatalf("deleted heartbeat: %+v %v", status, err)
	}
}
