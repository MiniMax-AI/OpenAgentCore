package store_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestManagedCapabilitiesWaitBeforeInitializationClaim(t *testing.T) {
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, sessions.CreateSession{
		Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration:  json.RawMessage(`{"environment":{"type":"openai_hosted"}}`),
		Initialization: environmentconfig.Setup{CapabilityDirectories: []string{"/workspace/generated"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider := &initializingProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, initializationPeer: initializationPeer{deferred: true}}
	key := uuid.NewString()
	worker, _ := managedWorkerMode(t, s, db, key, provider, false, true)
	owner, err := worker.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err != nil || initializationState(t, s, owner.TenantID, owner.EnvironmentID) != "pending" {
		t.Fatal(owner, err)
	}
	for range 4 {
		if err := worker.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	owner, err = s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
	if err != nil || initializationState(t, s, owner.TenantID, owner.EnvironmentID) != "pending" || owner.State != "running" || provider.writes.Load() != 0 || provider.kills != 0 {
		t.Fatal("missing socket consumed initialization or requested cleanup", owner, err, provider.writes.Load(), provider.kills)
	}
	if _, err := s.GetSessionExecutionBinding(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("ordinary readiness gate bypassed", err)
	}
}
