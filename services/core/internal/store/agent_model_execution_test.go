package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/google/uuid"
)

func agentProviderFixture(sequence int) *v1.ModelProviderInput {
	return &v1.ModelProviderInput{Protocol: "responses", BaseURL: fmt.Sprintf("https://provider-%d.example/v1", sequence), APIKey: fmt.Sprintf("private-agent-canary-%d", sequence)}
}

func agentProviderConfiguration(t *testing.T, provider *v1.ModelProviderInput, harness string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"model": "actual-model", "x_agents_core": v1.SavedAgentCore{Harness: harness, ModelProvider: provider.SafeView()}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAgentModelExecutionAtomicEncryptedSnapshot(t *testing.T) {
	_, pool := testStore(t)
	c, err := credentialcrypto.New(bytes.Repeat([]byte{31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, c)
	ctx, tenant := t.Context(), uuid.NewString()
	provider := agentProviderFixture(0)
	input := CreateAgentInput{Configuration: agentProviderConfiguration(t, provider, "codex"), ModelProvider: provider}
	agent, err := s.CreateAgent(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := pool.QueryRow(ctx, "SELECT encrypted_config FROM agent_model_execution WHERE agent_id=$1", agent.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(provider.APIKey)) || bytes.Contains(agent.Configuration, []byte(provider.APIKey)) {
		t.Fatal("provider secret exposed")
	}
	if _, err := c.OpenAgentModelExecution(encrypted, uuid.NewString(), agent.ID); err == nil {
		t.Fatal("ciphertext was not tenant bound")
	}
	if _, err := c.OpenAgentModelExecution(encrypted, tenant, uuid.NewString()); err == nil {
		t.Fatal("ciphertext was not Agent bound")
	}
	if _, _, err := s.GetAgentForSession(ctx, uuid.NewString(), agent.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign tenant lookup succeeded")
	}
	_, inherited, err := s.GetAgentForSession(ctx, tenant, agent.ID, true)
	if err != nil || inherited == nil || *inherited != *provider {
		t.Fatal("provider snapshot mismatch", err)
	}
	session, err := s.CreateSession(ctx, tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: []byte(`{"agent":{"model":"actual-model"},"environment":{"type":"openai_hosted"}}`), ModelProvider: inherited})
	if err != nil {
		t.Fatal(err)
	}
	// Omitted provider updates do not require access to the encryption key.
	withoutKey := New(pool)
	if _, err := withoutKey.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: []byte(`{"model":"new-model"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := withoutKey.GetAgentForSession(ctx, tenant, agent.ID, false); err != nil {
		t.Fatal("explicit override required Agent decryption", err)
	}
	if _, _, err := withoutKey.GetAgentForSession(ctx, tenant, agent.ID, true); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("missing cipher accepted", err)
	}
	if _, err := withoutKey.CreateAgent(ctx, tenant, input); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("unencrypted Agent create accepted", err)
	}
	replacement := agentProviderFixture(1)
	if _, err := s.UpdateAgent(ctx, uuid.NewString(), agent.ID, UpdateAgentInput{Configuration: agentProviderConfiguration(t, replacement, "codex"), ModelProvider: replacement, ModelProviderSet: true}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign tenant replacement accepted", err)
	}
	if _, err := withoutKey.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: agentProviderConfiguration(t, replacement, "codex"), ModelProvider: replacement, ModelProviderSet: true}); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("unencrypted replacement accepted", err)
	}
	current, inherited, err := s.GetAgentForSession(ctx, tenant, agent.ID, true)
	if err != nil || inherited == nil || *inherited != *provider || !bytes.Contains(current.Configuration, []byte("new-model")) {
		t.Fatal("failed replacement changed snapshot", err)
	}
	// A database rejection after secret replacement rolls both writes back.
	invalidPatch, _ := json.Marshal(map[string]any{"model": "invalid\x00model", "x_agents_core": v1.SavedAgentCore{Harness: "codex", ModelProvider: replacement.SafeView()}})
	if _, err := s.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: invalidPatch, ModelProvider: replacement, ModelProviderSet: true}); err == nil {
		t.Fatal("unstorable configuration accepted")
	}
	current, inherited, err = s.GetAgentForSession(ctx, tenant, agent.ID, true)
	if err != nil || inherited == nil || *inherited != *provider || !bytes.Contains(current.Configuration, []byte("new-model")) {
		t.Fatal("database rejection left a partial replacement", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM agents WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed create persisted partial Agent", err)
	}
	if _, err := s.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: []byte(`{"x_agents_core":{"harness":"claude_sdk"}}`)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("incompatible Harness-only update accepted", err)
	}
	// Provider-only replacement preserves the existing harness.
	patch, _ := json.Marshal(map[string]any{"x_agents_core": map[string]any{"model_provider": replacement.SafeView()}})
	if _, err := s.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: patch, ModelProvider: replacement, ModelProviderSet: true}); err != nil {
		t.Fatal(err)
	}
	current, inherited, err = s.GetAgentForSession(ctx, tenant, agent.ID, true)
	if err != nil || inherited == nil || *inherited != *replacement || !bytes.Contains(current.Configuration, []byte(`"harness": "codex"`)) {
		t.Fatal("provider-only replacement failed", err)
	}
	if _, err := withoutKey.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: []byte(`{"x_agents_core":{"harness":"codex"}}`)}); err != nil {
		t.Fatal(err)
	}
	_, inherited, err = s.GetAgentForSession(ctx, tenant, agent.ID, true)
	if err != nil || inherited == nil || *inherited != *replacement {
		t.Fatal("harness-only update lost provider", err)
	}
	if _, err := withoutKey.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: []byte(`{"x_agents_core":{"model_provider":null}}`), ModelProviderSet: true}); err != nil {
		t.Fatal(err)
	}
	current, inherited, err = s.GetAgentForSession(ctx, tenant, agent.ID, true)
	if err != nil || inherited != nil || !bytes.Contains(current.Configuration, []byte(`"harness": "codex"`)) {
		t.Fatal("clear lost harness or retained provider", err)
	}
	if _, err := s.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: agentProviderConfiguration(t, replacement, "codex"), ModelProvider: replacement, ModelProviderSet: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: []byte(`{"x_agents_core":null}`), ModelProviderSet: true}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM agent_model_execution WHERE agent_id=$1", agent.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("extension clear retained secret", err)
	}
	if _, err := s.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: agentProviderConfiguration(t, replacement, "codex"), ModelProvider: replacement, ModelProviderSet: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteAgent(ctx, tenant, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM agent_model_execution WHERE agent_id=$1", agent.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("Agent delete retained secret", err)
	}
	frozen, err := s.SessionModelExecution(ctx, tenant, session.ID)
	if err != nil || frozen == nil || *frozen != *provider {
		t.Fatal("Agent mutation changed Session snapshot", err)
	}
}

func TestAgentModelExecutionConcurrentSnapshots(t *testing.T) {
	_, pool := testStore(t)
	c, err := credentialcrypto.New(bytes.Repeat([]byte{32}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, c)
	ctx, tenant := t.Context(), uuid.NewString()
	p := agentProviderFixture(0)
	agent, err := s.CreateAgent(ctx, tenant, CreateAgentInput{Configuration: agentProviderConfiguration(t, p, "codex"), ModelProvider: p})
	if err != nil {
		t.Fatal(err)
	}
	configurations := make([]json.RawMessage, 31)
	for i := range configurations {
		configurations[i] = agentProviderConfiguration(t, agentProviderFixture(i), "codex")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i <= 30; i++ {
			p := agentProviderFixture(i)
			if _, err := s.UpdateAgent(ctx, tenant, agent.ID, UpdateAgentInput{Configuration: configurations[i], ModelProvider: p, ModelProviderSet: true}); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 80; i++ {
			a, p, err := s.GetAgentForSession(ctx, tenant, agent.ID, true)
			if err != nil {
				failures <- err
				return
			}
			var config struct {
				Core v1.SavedAgentCore `json:"x_agents_core"`
			}
			if json.Unmarshal(a.Configuration, &config) != nil || p == nil || config.Core.ModelProvider == nil || config.Core.ModelProvider.BaseURL != p.BaseURL {
				failures <- errors.New("concurrent read mixed safe and secret snapshots")
				return
			}
		}
	}()
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
