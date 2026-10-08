package integration

import (
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func localEnvironment(t *testing.T, s *Store, tenant string) (sessions.Session, sessions.Environment) {
	t.Helper()
	session, err := s.CreateSession(t.Context(), tenant, sessions.CreateSession{
		Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := sessionAdapter(s).GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return session, environment
}

func TestEnvironmentDeviceAuthorityAndLifecycle(t *testing.T) {
	s, pool := testStore(t)
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	session, environment := localEnvironment(t, s, tenant)
	sibling, _ := localEnvironment(t, s, tenant)
	foreign, _ := localEnvironment(t, s, foreignTenant)
	digest := runtimedevice.HashCredential(uuid.NewString())
	if _, err := FixtureEnvironmentDevice(t, t.Context(), pool, foreignTenant, environment.ID, "foreign", digest); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("foreign provisioning: %v", err)
	}
	bound, err := FixtureEnvironmentDevice(t, t.Context(), pool, tenant, environment.ID, "dedicated", digest)
	if err != nil {
		t.Fatalf("provision: %+v %v", bound, err)
	}
	// Sessions are placed on agent hosts only: the device binds no Session.
	execution := sessionExecution(t, executionWriter(t, s).lease)
	for _, other := range []sessions.Session{session, sibling, foreign} {
		if err := execution.BindSessionDevice(t.Context(), other.TenantID, other.ID, bound.ID); err == nil {
			t.Fatal("an Environment device was bound to a Session")
		}
	}
	if _, err := sessionAdapter(s).GetSessionDevice(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("provisioning bound the Session: %v", err)
	}
	hosts, err := sessionAdapter(s).ListAgentHosts(t.Context(), tenant)
	if err != nil || slices.ContainsFunc(hosts, func(host sessions.ExecutionDevice) bool { return host.ID == bound.ID }) {
		t.Fatalf("an Environment device entered placement: %v %v", hosts, err)
	}
	if _, ok, err := sessionAdapter(s).GetDeviceCredential(t.Context(), bound.ID); err != nil || !ok {
		t.Fatalf("valid credential unavailable: %v", err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := sessionAdapter(s).GetDeviceCredential(t.Context(), bound.ID); err != nil || ok {
		t.Fatalf("deleted Environment still authenticates: %v", err)
	}
	status, err := sessionService(t, s).TouchRuntimeHeartbeat(t.Context(), bound.ID)
	if err != nil || !status.Deleted {
		t.Fatalf("deleted Environment heartbeat: %+v %v", status, err)
	}
}

func TestEnvironmentDeviceProvisioningHasOneWinner(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	_, environment := localEnvironment(t, s, tenant)
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := FixtureEnvironmentDevice(t, t.Context(), pool, tenant, environment.ID, "runtime", runtimedevice.HashCredential(uuid.NewString()))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, sessions.ErrDeviceBindingConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("provisioned %d devices", winners)
	}
	var device string
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM devices WHERE environment_id = $1", environment.ID).Scan(&device); err != nil {
		t.Fatal(err)
	}
	if err := sessionService(t, s).RevokeDevice(t.Context(), tenant, device); err != nil {
		t.Fatal(err)
	}
	if _, err := FixtureEnvironmentDevice(t, t.Context(), pool, tenant, environment.ID, "replacement", runtimedevice.HashCredential(uuid.NewString())); !errors.Is(err, sessions.ErrDeviceBindingConflict) {
		t.Fatalf("silent placement replacement: %v", err)
	}
}
