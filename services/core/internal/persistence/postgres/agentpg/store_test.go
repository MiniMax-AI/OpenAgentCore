package agentpg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/agentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// open returns the adapter and the Agent service over pool.
func open(t *testing.T, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) (*agentpg.Store, *agents.Service) {
	t.Helper()
	store := agentpg.New(pgunit.NewPool(pool), cipher)
	service, err := agents.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	return store, service
}

func testCipher(t *testing.T, seed byte) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{seed}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func providerFixture(sequence int) *v1.ModelProviderInput {
	return &v1.ModelProviderInput{Protocol: "responses", BaseURL: fmt.Sprintf("https://provider-%d.example/v1", sequence), APIKey: fmt.Sprintf("private-agent-canary-%d", sequence)}
}

func providerConfiguration(t *testing.T, provider *v1.ModelProviderInput, harness string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"model": "actual-model", "x_agents_core": v1.SavedAgentCore{Harness: harness, ModelProvider: provider.SafeView()}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAgentsPersistIndependentlyAndStayTenantScoped(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool, nil)
	ctx := t.Context()
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	// Configuration is preserved without applying one harness's capabilities.
	command := agents.CreateCommand{
		TenantID: tenantA, Metadata: map[string]string{"purpose": "保存 configuration"},
		Configuration: []byte(`{"model":" caller-model ","name":null,"instructions":" keep whitespace ","multi_agent":{"enabled":true,"max_concurrent_subagents":6},"tools":[{"type":"function","name":"lookup","description":"","defer_loading":true,"parameters":{"type":"object","properties":{"number":{"const":9007199254740993}}}}]}`),
	}
	before := time.Now().Add(-time.Second)
	first, err := service.Create(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if json.Unmarshal(command.Configuration, &want) != nil || json.Unmarshal(first.Configuration, &got) != nil || !reflect.DeepEqual(got, want) ||
		!bytes.Contains(first.Configuration, []byte("9007199254740993")) {
		t.Fatalf("configuration changed: %s", first.Configuration)
	}
	if first.TenantID != tenantA || !reflect.DeepEqual(first.Metadata, command.Metadata) ||
		first.CreatedAt.Before(before) || first.CreatedAt.After(time.Now().Add(time.Second)) || !first.CreatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("unexpected saved agent: %+v", first)
	}
	// Identical configurations are distinct resources; storage invents no public
	// create-idempotency contract or shared identity across callers.
	for _, tenant := range []string{tenantA, tenantB} {
		command.TenantID = tenant
		other, err := service.Create(ctx, command)
		if err != nil || other.ID == first.ID || other.TenantID != tenant {
			t.Fatalf("distinct create: %+v, %v", other, err)
		}
	}
	for _, lookup := range []struct{ tenant, id string }{{tenantB, first.ID}, {tenantA, uuid.NewString()}, {tenantA, "not-an-id"}, {tenantA, uuid.Nil.String()}} {
		if _, err := store.GetAgent(ctx, lookup.tenant, lookup.id); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("unowned/absent agent read: %v", err)
		}
	}
	reopened, _ := open(t, pgtest.Open(t), nil)
	pool.Close()
	durable, err := reopened.GetAgent(ctx, tenantA, first.ID)
	if err != nil || !reflect.DeepEqual(durable, first) {
		t.Fatalf("durable read: %+v, %v; want %+v", durable, err, first)
	}
}

func TestAgentsRejectInvalidTenantsAndEmptyMetadataIsAMap(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool, nil)
	ctx := t.Context()
	tenant := uuid.NewString()
	valid := agents.CreateCommand{Configuration: []byte(`{"model":"x"}`)}
	for _, invalid := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		valid.TenantID = invalid
		if _, err := service.Create(ctx, valid); !errors.Is(err, agents.ErrInvalidInput) {
			t.Fatalf("invalid tenant accepted: %v", err)
		}
		if _, err := store.GetAgent(ctx, invalid, uuid.NewString()); !errors.Is(err, agents.ErrInvalidInput) {
			t.Fatalf("invalid read tenant accepted: %v", err)
		}
		if _, err := store.ListAgents(ctx, agents.ListQuery{TenantID: invalid, Limit: 1}); !errors.Is(err, agents.ErrInvalidInput) {
			t.Fatalf("invalid list tenant accepted: %v", err)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM agents WHERE tenant_id = $1", tenant); n != 0 {
		t.Fatalf("rejected input wrote %d rows", n)
	}
	valid.TenantID = tenant
	empty, err := service.Create(ctx, valid)
	if err != nil || empty.Metadata == nil || len(empty.Metadata) != 0 {
		t.Fatalf("empty metadata: %+v, %v", empty, err)
	}
}

func TestAgentListPaginationIsolationAndReconnect(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool, nil)
	ctx := t.Context()
	tenant, other := uuid.NewString(), uuid.NewString()
	empty, err := store.ListAgents(ctx, agents.ListQuery{TenantID: tenant, Limit: 2})
	if err != nil || empty.Agents == nil || len(empty.Agents) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty page: %+v, %v", empty, err)
	}
	command := agents.CreateCommand{TenantID: tenant, Configuration: []byte(`{"model":"unchanged","tools":[{"parameters":{"const":9007199254740993}}]}`), Metadata: map[string]string{"scope": "same-tenant"}}
	ids := []string{}
	for range 5 {
		agent, err := service.Create(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, agent.ID)
	}
	command.TenantID = other
	foreign, err := service.Create(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0).UTC()
	if _, err := pool.Exec(ctx, "UPDATE agents SET created_at=$1, updated_at=$1 WHERE tenant_id=$2", stamp, tenant); err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	read := func(s *agentpg.Store, ascending bool) []string {
		t.Helper()
		var actual []string
		cursor := ""
		for {
			page, err := s.ListAgents(ctx, agents.ListQuery{TenantID: tenant, After: cursor, Limit: 2, Ascending: ascending})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Agents) == 0 || len(page.Agents) > 2 {
				t.Fatalf("bad page: %+v", page)
			}
			for _, agent := range page.Agents {
				original, err := s.GetAgent(ctx, tenant, agent.ID)
				if err != nil || !reflect.DeepEqual(agent, original) || agent.TenantID != tenant {
					t.Fatalf("resource changed: %+v, %v", agent, err)
				}
				actual = append(actual, agent.ID)
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Agents[len(page.Agents)-1].ID || len(actual) > len(ids) {
				t.Fatal("invalid/repeating continuation")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	if got := read(store, true); !slices.Equal(got, ids) {
		t.Fatalf("ascending equal timestamps: %v", got)
	}
	reverse := slices.Clone(ids)
	slices.Reverse(reverse)
	if got := read(store, false); !slices.Equal(got, reverse) {
		t.Fatalf("descending equal timestamps: %v", got)
	}
	// A malformed cursor follows the missing-cursor path (ERR-01).
	for _, after := range []string{foreign.ID, uuid.NewString(), "not-an-id"} {
		if _, err := store.ListAgents(ctx, agents.ListQuery{TenantID: tenant, After: after, Limit: 2, Ascending: true}); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("unowned/unknown/malformed cursor accepted: %v", err)
		}
	}
	tail, err := store.ListAgents(ctx, agents.ListQuery{TenantID: tenant, After: ids[len(ids)-1], Limit: 2, Ascending: true})
	if err != nil || tail.Agents == nil || len(tail.Agents) != 0 || tail.NextCursor != "" {
		t.Fatalf("end page: %+v, %v", tail, err)
	}
	foreignPage, err := store.ListAgents(ctx, agents.ListQuery{TenantID: other, Limit: 100})
	if err != nil || len(foreignPage.Agents) != 1 || foreignPage.Agents[0].ID != foreign.ID {
		t.Fatalf("tenant isolation: %+v, %v", foreignPage, err)
	}
	restored, _ := open(t, pgtest.Open(t), nil)
	pool.Close()
	if got := read(restored, true); !slices.Equal(got, ids) {
		t.Fatalf("pagination changed after reconnect: %v", got)
	}
}

func TestAgentUpdateRollbackAndCompleteSizeBound(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool, nil)
	ctx := t.Context()
	tenant := uuid.NewString()
	configuration, err := json.Marshal(map[string]any{"model": "original", "instructions": strings.Repeat("x", 400*1024), "number": json.Number("9007199254740993")})
	if err != nil {
		t.Fatal(err)
	}
	original, err := service.Create(ctx, agents.CreateCommand{TenantID: tenant, Configuration: configuration, Metadata: map[string]string{"keep": "original"}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"replace": "not-committed"}
	oversized, err := json.Marshal(map[string]string{"name": strings.Repeat("y", 150*1024)})
	if err != nil {
		t.Fatal(err)
	}
	// The merged configuration exceeds the bound only after the locked read.
	if _, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: original.ID, Configuration: oversized, Metadata: &metadata}); !errors.Is(err, agents.ErrInvalidInput) {
		t.Fatalf("oversized merged update: %v", err)
	}
	if unchanged, err := store.GetAgent(ctx, tenant, original.ID); err != nil || !reflect.DeepEqual(unchanged, original) {
		t.Fatalf("partial failed update: %v", err)
	}
	for _, id := range []string{original.ID, "not-an-id"} {
		_, err = service.Update(ctx, agents.UpdateCommand{TenantID: uuid.NewString(), AgentID: id, Configuration: []byte(`{"model":"foreign"}`), Metadata: &metadata})
		if !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("foreign or malformed update: %v", err)
		}
	}
	// A failure releases the lock; a later valid patch preserves unrelated large values and numbers.
	updated, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: original.ID, Configuration: []byte(`{"model":"updated"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(updated.Configuration, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["number"]) != "9007199254740993" || string(fields["model"]) != `"updated"` || !reflect.DeepEqual(updated.Metadata, original.Metadata) {
		t.Fatal("unrelated configuration or metadata lost")
	}
	if !updated.CreatedAt.Equal(original.CreatedAt) || updated.UpdatedAt.Before(original.UpdatedAt) {
		t.Fatal("resource timestamps changed incorrectly")
	}
	unchanged, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: original.ID})
	if err != nil || !unchanged.UpdatedAt.After(updated.UpdatedAt) {
		t.Fatalf("empty update did not advance timestamp: %v", err)
	}
	updated.UpdatedAt = unchanged.UpdatedAt
	if !reflect.DeepEqual(unchanged, updated) {
		t.Fatal("empty update changed saved configuration")
	}
}

func TestAgentDeleteIsTenantScoped(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool, nil)
	ctx := t.Context()
	tenant := uuid.NewString()
	agent, err := service.Create(ctx, agents.CreateCommand{TenantID: tenant, Configuration: []byte(`{"model":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []agents.DeleteCommand{{TenantID: uuid.NewString(), AgentID: agent.ID}, {TenantID: tenant, AgentID: "not-an-id"}} {
		if _, err := service.Delete(ctx, target); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("foreign or malformed delete: %v", err)
		}
	}
	if id, err := service.Delete(ctx, agents.DeleteCommand{TenantID: tenant, AgentID: agent.ID}); err != nil || id != agent.ID {
		t.Fatalf("delete = %s, %v", id, err)
	}
	if _, err := store.GetAgent(ctx, tenant, agent.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("deleted agent read: %v", err)
	}
	if _, err := service.Delete(ctx, agents.DeleteCommand{TenantID: tenant, AgentID: agent.ID}); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("repeated delete: %v", err)
	}
}

func TestAgentModelExecutionAtomicEncryptedSnapshot(t *testing.T) {
	pool := pgtest.Open(t)
	c := testCipher(t, 31)
	store, service := open(t, pool, c)
	ctx, tenant := t.Context(), uuid.NewString()
	provider := providerFixture(0)
	create := agents.CreateCommand{TenantID: tenant, Configuration: providerConfiguration(t, provider, "codex"), ModelProvider: provider}
	agent, err := service.Create(ctx, create)
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
	if _, _, err := store.GetAgentWithModelProvider(ctx, uuid.NewString(), agent.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatal("foreign tenant lookup succeeded")
	}
	snapshot := func(s *agentpg.Store) (agents.Agent, *v1.ModelProviderInput) {
		t.Helper()
		current, inherited, err := s.GetAgentWithModelProvider(ctx, tenant, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		return current, inherited
	}
	if _, inherited := snapshot(store); inherited == nil || *inherited != *provider {
		t.Fatal("provider snapshot mismatch")
	}
	// Omitted provider updates and plain reads do not require the encryption key.
	keylessStore, keyless := open(t, pool, nil)
	if _, err := keyless.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: []byte(`{"model":"new-model"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := keylessStore.GetAgent(ctx, tenant, agent.ID); err != nil {
		t.Fatal("plain read required Agent decryption", err)
	}
	if _, _, err := keylessStore.GetAgentWithModelProvider(ctx, tenant, agent.ID); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("missing key opened the bundle", err)
	}
	if _, _, err := agentpg.New(pgunit.NewPool(pool), testCipher(t, 99)).GetAgentWithModelProvider(ctx, tenant, agent.ID); err == nil || err.Error() != "agent model provider decryption failed" {
		t.Fatal("wrong key was not a decryption failure", err)
	}
	if _, err := keyless.Create(ctx, create); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("unencrypted Agent create accepted", err)
	}
	replacement := providerFixture(1)
	replace := func(tenant string) agents.UpdateCommand {
		return agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: providerConfiguration(t, replacement, "codex"), ModelProvider: &agents.ModelProviderChange{Provider: replacement}}
	}
	if _, err := service.Update(ctx, replace(uuid.NewString())); !errors.Is(err, agents.ErrNotFound) {
		t.Fatal("foreign tenant replacement accepted", err)
	}
	if _, err := keyless.Update(ctx, replace(tenant)); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("unencrypted replacement accepted", err)
	}
	if current, inherited := snapshot(store); inherited == nil || *inherited != *provider || !bytes.Contains(current.Configuration, []byte("new-model")) {
		t.Fatal("failed replacement changed snapshot")
	}
	// A database rejection after secret replacement rolls both writes back.
	invalidPatch, _ := json.Marshal(map[string]any{"model": "invalid\x00model", "x_agents_core": v1.SavedAgentCore{Harness: "codex", ModelProvider: replacement.SafeView()}})
	if _, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: invalidPatch, ModelProvider: &agents.ModelProviderChange{Provider: replacement}}); !errors.Is(err, textvalue.ErrUnstorable) {
		t.Fatal("unstorable configuration accepted", err)
	}
	if current, inherited := snapshot(store); inherited == nil || *inherited != *provider || !bytes.Contains(current.Configuration, []byte("new-model")) {
		t.Fatal("database rejection left a partial replacement")
	}
	if n := count(t, pool, "SELECT count(*) FROM agents WHERE tenant_id=$1", tenant); n != 1 {
		t.Fatal("failed create persisted partial Agent", n)
	}
	if _, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: []byte(`{"x_agents_core":{"harness":"claude_sdk"}}`)}); !errors.Is(err, agents.ErrInvalidInput) {
		t.Fatal("incompatible Harness-only update accepted", err)
	}
	// Provider-only replacement preserves the existing harness.
	patch, _ := json.Marshal(map[string]any{"x_agents_core": map[string]any{"model_provider": replacement.SafeView()}})
	if _, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: patch, ModelProvider: &agents.ModelProviderChange{Provider: replacement}}); err != nil {
		t.Fatal(err)
	}
	if current, inherited := snapshot(store); inherited == nil || *inherited != *replacement || !bytes.Contains(current.Configuration, []byte(`"harness": "codex"`)) {
		t.Fatal("provider-only replacement failed")
	}
	if _, err := keyless.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: []byte(`{"x_agents_core":{"harness":"codex"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, inherited := snapshot(store); inherited == nil || *inherited != *replacement {
		t.Fatal("harness-only update lost provider")
	}
	if _, err := keyless.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: []byte(`{"x_agents_core":{"model_provider":null}}`), ModelProvider: &agents.ModelProviderChange{}}); err != nil {
		t.Fatal(err)
	}
	if current, inherited := snapshot(store); inherited != nil || !bytes.Contains(current.Configuration, []byte(`"harness": "codex"`)) {
		t.Fatal("clear lost harness or retained provider")
	}
	if _, err := service.Update(ctx, replace(tenant)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: []byte(`{"x_agents_core":null}`), ModelProvider: &agents.ModelProviderChange{}}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM agent_model_execution WHERE agent_id=$1", agent.ID); n != 0 {
		t.Fatal("extension clear retained secret")
	}
	if _, err := service.Update(ctx, replace(tenant)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Delete(ctx, agents.DeleteCommand{TenantID: tenant, AgentID: agent.ID}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM agent_model_execution WHERE agent_id=$1", agent.ID); n != 0 {
		t.Fatal("Agent delete retained secret")
	}
}

// A bundle sealed to another Agent is a decryption failure, never a missing
// key or a missing bundle.
func TestAgentBundleSealedToAnotherAgentDoesNotOpen(t *testing.T) {
	pool := pgtest.Open(t)
	c := testCipher(t, 32)
	store, service := open(t, pool, c)
	ctx, tenant := t.Context(), uuid.NewString()
	provider := providerFixture(0)
	agent, err := service.Create(ctx, agents.CreateCommand{TenantID: tenant, Configuration: providerConfiguration(t, provider, "codex"), ModelProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.SealAgentModelExecution(raw, tenant, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE agent_model_execution SET encrypted_config=$2 WHERE agent_id=$1", agent.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, inherited, err := store.GetAgentWithModelProvider(ctx, tenant, agent.ID); err == nil || err.Error() != "agent model provider decryption failed" || inherited != nil {
		t.Fatal("a wrong binding was not a decryption failure", err)
	}
}

func TestAgentModelExecutionConcurrentSnapshots(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool, testCipher(t, 32))
	ctx, tenant := t.Context(), uuid.NewString()
	p := providerFixture(0)
	agent, err := service.Create(ctx, agents.CreateCommand{TenantID: tenant, Configuration: providerConfiguration(t, p, "codex"), ModelProvider: p})
	if err != nil {
		t.Fatal(err)
	}
	configurations := make([]json.RawMessage, 31)
	for i := range configurations {
		configurations[i] = providerConfiguration(t, providerFixture(i), "codex")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i <= 30; i++ {
			change := &agents.ModelProviderChange{Provider: providerFixture(i)}
			if _, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: agent.ID, Configuration: configurations[i], ModelProvider: change}); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 80; i++ {
			a, p, err := store.GetAgentWithModelProvider(ctx, tenant, agent.ID)
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

func auditContext(ctx context.Context, tenant, request, key string) context.Context {
	return writeaudit.WithSource(ctx, writeaudit.Source{
		KeyID: "static:" + strings.Repeat(key, 64), Name: "agent audit fixture", Prefix: strings.Repeat(key, 8),
		Kind: "static", TenantID: tenant, RequestID: request, TraceID: "agent-audit-trace",
	})
}

// A trigger fails the audit insertion after each real mutation; comparing the
// tenant's rows proves the Agent, its sealed bundle and the audit roll back
// together. An administrator delete records administrator audit only.
func TestAgentWritesAuditInTheirTransaction(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	_, service := open(t, pool, testCipher(t, 91))
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_agent_audit_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.request_id = 'reject-agent-audit' THEN RAISE EXCEPTION 'forced audit insertion failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_agent_audit_fixture BEFORE INSERT ON write_audit_operations FOR EACH ROW EXECUTE FUNCTION reject_agent_audit_fixture();
	CREATE TRIGGER reject_agent_admin_audit_fixture BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_agent_audit_fixture()`); err != nil {
		t.Fatal(err)
	}
	snapshot := func(tenant string) string {
		t.Helper()
		var rows string
		if err := pool.QueryRow(ctx, `SELECT concat_ws('|',
			(SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY a.id)::text, '') FROM agents a WHERE a.tenant_id=$1),
			(SELECT COALESCE(jsonb_agg(to_jsonb(m) ORDER BY m.agent_id)::text, '') FROM agent_model_execution m JOIN agents a ON a.id=m.agent_id WHERE a.tenant_id=$1),
			(SELECT count(*)::text FROM write_audit_operations WHERE tenant_id=$1),
			(SELECT count(*)::text FROM write_audit_owners WHERE tenant_id=$1),
			(SELECT count(*)::text FROM admin_audit_log WHERE tenant_id=$1))`, tenant).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	provider := providerFixture(0)
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			tenant := uuid.NewString()
			create := agents.CreateCommand{TenantID: tenant, Configuration: providerConfiguration(t, provider, "codex"), ModelProvider: provider}
			run := func(ctx context.Context) (string, error) {
				a, err := service.Create(ctx, create)
				return a.ID, err
			}
			owners := 1
			if action != "create" {
				existing, err := service.Create(ctx, create)
				if err != nil {
					t.Fatal(err)
				}
				owners = 0
				run = func(ctx context.Context) (string, error) {
					if action == "delete" {
						return service.Delete(ctx, agents.DeleteCommand{TenantID: tenant, AgentID: existing.ID})
					}
					change := &agents.ModelProviderChange{Provider: providerFixture(1)}
					a, err := service.Update(ctx, agents.UpdateCommand{TenantID: tenant, AgentID: existing.ID, Configuration: providerConfiguration(t, change.Provider, "codex"), ModelProvider: change})
					return a.ID, err
				}
			}
			before := snapshot(tenant)
			if _, err := run(auditContext(ctx, tenant, "reject-agent-audit", "a")); err == nil {
				t.Fatal("audit failure was accepted")
			}
			if after := snapshot(tenant); after != before {
				t.Fatal("audit failure left business or audit changes")
			}
			request := uuid.NewString()
			id, err := run(auditContext(ctx, tenant, request, "a"))
			if err != nil {
				t.Fatal(err)
			}
			var gotAction, kind, gotID string
			var parent *string
			if err := pool.QueryRow(ctx, `SELECT action,resource_type,resource_id,parent_id FROM write_audit_operations WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&gotAction, &kind, &gotID, &parent); err != nil {
				t.Fatal(err)
			}
			if gotAction != action || kind != "agent" || gotID != id || parent != nil && *parent != "" {
				t.Fatalf("wrong operation identity: %s %s %s %v", gotAction, kind, gotID, parent)
			}
			if n := count(t, pool, `SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1`, tenant); n != owners {
				t.Fatalf("ownership count %d, want %d", n, owners)
			}
			if n := count(t, pool, `SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1 AND to_jsonb(write_audit_operations)::text LIKE '%private-agent-canary%'`, tenant); n != 0 {
				t.Fatal("audit contains secret")
			}
		})
	}
	t.Run("administrator delete", func(t *testing.T) {
		tenant := uuid.NewString()
		// Administrator audit names the Project that owns the tenant.
		if _, err := pool.Exec(ctx, "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'agent-admin',$2)", tenant, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Agent fixture',$1,'service_account',$2)", tenant, "project:"+tenant); err != nil {
			t.Fatal(err)
		}
		agent, err := service.Create(ctx, agents.CreateCommand{TenantID: tenant, Configuration: providerConfiguration(t, provider, "codex"), ModelProvider: provider})
		if err != nil {
			t.Fatal(err)
		}
		// Administrator provenance takes precedence over an inherited public one.
		adminContext := func(request string) context.Context {
			return adminaudit.WithSource(auditContext(ctx, tenant, request, "a"), adminaudit.Source{
				CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: tenant, RequestID: request, TraceID: "admin-trace",
			})
		}
		before := snapshot(tenant)
		if _, err := service.Delete(adminContext("reject-agent-audit"), agents.DeleteCommand{TenantID: tenant, AgentID: agent.ID}); err == nil {
			t.Fatal("administrator audit failure was accepted")
		}
		if snapshot(tenant) != before {
			t.Fatal("administrator audit failure left business or audit changes")
		}
		request := uuid.NewString()
		if _, err := service.Delete(adminContext(request), agents.DeleteCommand{TenantID: tenant, AgentID: agent.ID}); err != nil {
			t.Fatal(err)
		}
		var action, kind, id, raw string
		if err := pool.QueryRow(ctx, `SELECT action,resource_type,resource_id,to_jsonb(a)::text FROM admin_audit_log a WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&action, &kind, &id, &raw); err != nil {
			t.Fatal(err)
		}
		if action != "delete" || kind != "agent" || id != agent.ID || strings.Contains(raw, "private-agent-canary") {
			t.Fatalf("administrator audit = %s", raw)
		}
		if n := count(t, pool, `SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1`, tenant); n != 0 {
			t.Fatal("administrator impersonated public-key provenance")
		}
		if n := count(t, pool, `SELECT count(*) FROM agent_model_execution WHERE agent_id=$1`, agent.ID); n != 0 {
			t.Fatal("administrator delete retained the sealed bundle")
		}
	})
}

// Reads and failed writes record nothing, and the creator stays the owner
// after another key updates and deletes the Agent.
func TestAgentAuditReadsFailuresAndStableOwnership(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool, nil)
	tenant := uuid.NewString()
	first, err := service.Create(auditContext(t.Context(), tenant, uuid.NewString(), "a"), agents.CreateCommand{TenantID: tenant, Configuration: []byte(`{"model":"fixture"}`)})
	if err != nil {
		t.Fatal(err)
	}
	owner := func() string {
		var row string
		if err := pool.QueryRow(t.Context(), `SELECT to_jsonb(o)::text FROM write_audit_owners o WHERE tenant_id=$1 AND resource_id=$2`, tenant, first.ID).Scan(&row); err != nil {
			t.Fatal("creator disappeared", err)
		}
		return row
	}
	before := owner()
	readCtx := auditContext(t.Context(), tenant, uuid.NewString(), "a")
	if _, err := store.GetAgent(readCtx, tenant, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListAgents(readCtx, agents.ListQuery{TenantID: tenant, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(readCtx, agents.UpdateCommand{TenantID: tenant, AgentID: uuid.NewString()}); !errors.Is(err, agents.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := service.Update(auditContext(t.Context(), tenant, uuid.NewString(), "b"), agents.UpdateCommand{TenantID: tenant, AgentID: first.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Delete(auditContext(t.Context(), tenant, uuid.NewString(), "b"), agents.DeleteCommand{TenantID: tenant, AgentID: first.ID}); err != nil {
		t.Fatal(err)
	}
	if owner() != before {
		t.Fatal("creator changed")
	}
	if n := count(t, pool, `SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1`, tenant); n != 3 {
		t.Fatal("read or failure audit", n)
	}
}

// Malformed supplied provenance fails the write closed.
func TestAgentWriteRejectsInvalidAuditSource(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := open(t, pool, nil)
	tenant := uuid.NewString()
	ctx := writeaudit.WithSource(t.Context(), writeaudit.Source{KeyID: "static:" + strings.Repeat("a", 64), Prefix: "aaaaaaaa", Kind: "static", TenantID: tenant, RequestID: uuid.NewString()})
	if _, err := service.Create(ctx, agents.CreateCommand{TenantID: tenant, Configuration: []byte(`{"model":"x"}`)}); !errors.Is(err, writeaudit.ErrInvalidSource) {
		t.Fatalf("invalid source accepted: %v", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM agents WHERE tenant_id=$1", tenant); n != 0 {
		t.Fatal("invalid source wrote an Agent")
	}
}
