package integration

import (
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func runtimeEnrollmentFixture(t *testing.T, s *Store, p identity.Principal) (sessions.Session, sessions.Environment, sessions.IssuedExecutorCredential) {
	t.Helper()
	input := environmentInput(uuid.NewString(), "self_hosted", "/workspace")
	input.Creator = p.Subject()
	session, err := s.CreateSession(t.Context(), p.TenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := sessionAdapter(s).GetSessionEnvironment(t.Context(), p.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := sessionService(t, s).IssueExecutorCredential(t.Context(), p, uuid.NewString(), environment.ID)
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
		if _, err := sessionService(t, s).EnrollRuntime(ctx, target.ID, executorDigest(key.Token)); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatalf("foreign target enrollment: %v", err)
		}
	}
	bound, err := sessionService(t, s).EnrollRuntime(ctx, environment.ID, executorDigest(key.Token))
	if err != nil || bound.TenantID != p.TenantID || bound.EnvironmentID != environment.ID || bound.Kind != "enrollment" || bound.ID == "" || bound.Generation != 1 {
		t.Fatalf("enrollment: %+v %v", bound, err)
	}
	if again, err := sessionService(t, s).EnrollRuntime(ctx, environment.ID, executorDigest(key.Token)); err != nil || again != bound {
		t.Fatalf("retry changed the resource: %+v %v", again, err)
	}
	if _, err := sessionAdapter(s).GetSessionDevice(ctx, p.TenantID, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("enrollment bound the Session: %v", err)
	}
	if live, err := sessionAdapter(s).GetEnvironmentResource(ctx, p.TenantID, environment.ID); err != nil || live.Resource != bound || live.CredentialHash != executorDigest(key.Token) {
		t.Fatalf("live resource: %+v %v", live, err)
	}
	otherKey, err := sessionService(t, s).IssueExecutorCredential(ctx, p, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionService(t, s).EnrollRuntime(ctx, environment.ID, executorDigest(otherKey.Token)); !errors.Is(err, sessions.ErrDeviceBindingConflict) {
		t.Fatalf("another key replaced the enrollment: %v", err)
	}
	rotated, err := sessionService(t, s).RotateExecutorCredential(ctx, p, key.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionService(t, s).EnrollRuntime(ctx, environment.ID, executorDigest(key.Token)); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("the rotated-out token enrolled: %v", err)
	}
	next := bound
	next.Generation++
	if again, err := sessionService(t, s).EnrollRuntime(ctx, environment.ID, executorDigest(rotated.Token)); err != nil || again != next {
		t.Fatalf("re-enrollment after rotation: %+v %v", again, err)
	}
	if err := sessionService(t, s).RevokeExecutorCredential(ctx, p, key.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionAdapter(s).GetEnvironmentResource(ctx, p.TenantID, environment.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("a revoked key's enrollment is live: %v", err)
	}
}

func TestRuntimeEnrollmentConcurrentAndDeletion(t *testing.T) {
	s, pool := testStore(t)
	p := FixtureExecutorPrincipal(t, s, uuid.NewString())
	session, environment, key := runtimeEnrollmentFixture(t, s, p)
	var wg sync.WaitGroup
	results := make(chan sandboxbootstrap.Resource, 6)
	failures := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := sessionService(t, s).EnrollRuntime(t.Context(), environment.ID, executorDigest(key.Token))
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
	var bound sandboxbootstrap.Resource
	for got := range results {
		if bound.ID == "" {
			bound = got
		}
		if got != bound {
			t.Fatal("concurrent enrollment created multiple resources")
		}
	}
	var allocations int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE environment_id=$1", environment.ID).Scan(&allocations); err != nil || allocations != 0 {
		t.Fatalf("enrollment allocated compute: %d %v", allocations, err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: p.TenantID, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionService(t, s).EnrollRuntime(t.Context(), environment.ID, executorDigest(key.Token)); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("deleted enrollment: %v", err)
	}
	if _, err := sessionAdapter(s).GetEnvironmentResource(t.Context(), p.TenantID, environment.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("a deleted Session's enrollment is live: %v", err)
	}
}
