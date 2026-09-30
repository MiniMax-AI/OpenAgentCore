package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// fakeStorage is a strict Storage fake: a call whose func is nil fails the test.
type fakeStorage struct {
	t               *testing.T
	createAgent     func(context.Context, NewAgent) (Agent, error)
	withAgentUpdate func(context.Context, string, string, func(UpdateTx) error) error
	deleteAgent     func(context.Context, string, string) (string, error)
}

func (f *fakeStorage) CreateAgent(ctx context.Context, agent NewAgent) (Agent, error) {
	if f.createAgent == nil {
		f.t.Fatal("unexpected call to CreateAgent")
	}
	return f.createAgent(ctx, agent)
}

func (f *fakeStorage) WithAgentUpdate(ctx context.Context, tenantID, agentID string, decide func(UpdateTx) error) error {
	if f.withAgentUpdate == nil {
		f.t.Fatal("unexpected call to WithAgentUpdate")
	}
	return f.withAgentUpdate(ctx, tenantID, agentID, decide)
}

func (f *fakeStorage) DeleteAgent(ctx context.Context, tenantID, agentID string) (string, error) {
	if f.deleteAgent == nil {
		f.t.Fatal("unexpected call to DeleteAgent")
	}
	return f.deleteAgent(ctx, tenantID, agentID)
}

// fakeUpdateTx serves one locked Agent and records the applied revision.
type fakeUpdateTx struct {
	current Agent
	loadErr error
	applied *Revision
}

func (tx *fakeUpdateTx) LoadAgent() (Agent, error) { return tx.current, tx.loadErr }

func (tx *fakeUpdateTx) ApplyRevision(revision Revision) (Agent, error) {
	tx.applied = &revision
	return Agent{ID: tx.current.ID, Configuration: revision.Configuration}, nil
}

func newTestService(t *testing.T, storage *fakeStorage) *Service {
	t.Helper()
	storage.t = t
	service, err := NewService(storage)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewServiceRequiresStorage(t *testing.T) {
	if service, err := NewService(nil); err == nil || service != nil {
		t.Fatalf("NewService(nil) = %v, %v", service, err)
	}
}

func TestCreateValidatesBeforeStorage(t *testing.T) {
	service := newTestService(t, &fakeStorage{})
	for _, command := range []CreateCommand{
		{Configuration: json.RawMessage(`[]`)},
		{Configuration: json.RawMessage(`{"model":"a"}`), Metadata: map[string]string{"k": strings.Repeat("v", 70*1024)}},
		{Configuration: json.RawMessage(`{"model":"a","x_agents_core":{"harness":"claude_sdk","model_provider":` + testProvider + `}}`)},
	} {
		if _, err := service.Create(context.Background(), command); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Create = %v", err)
		}
	}
}

func TestCreatePassesNormalizedAgent(t *testing.T) {
	provider := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.test/v1", APIKey: "secret"}
	var saved NewAgent
	service := newTestService(t, &fakeStorage{createAgent: func(_ context.Context, agent NewAgent) (Agent, error) {
		saved = agent
		return Agent{ID: "agent"}, nil
	}})
	if _, err := service.Create(context.Background(), CreateCommand{TenantID: "tenant", Configuration: json.RawMessage(`{ "model": "a" }`), ModelProvider: provider}); err != nil {
		t.Fatal(err)
	}
	if saved.TenantID != "tenant" || string(saved.Configuration) != `{"model":"a"}` || string(saved.Metadata) != `{}` || saved.ModelProvider != provider {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestUpdateValidatesBeforeLookup(t *testing.T) {
	service := newTestService(t, &fakeStorage{})
	large := map[string]string{"k": strings.Repeat("v", 70*1024)}
	for _, command := range []UpdateCommand{
		{Configuration: json.RawMessage(`[]`)},
		{Configuration: json.RawMessage(`{} {}`)},
		{Metadata: &large},
	} {
		if _, err := service.Update(context.Background(), command); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Update = %v", err)
		}
	}
}

func TestUpdateDecidesOverLockedAgent(t *testing.T) {
	current := Agent{ID: "agent", Metadata: map[string]string{"team": "core"},
		Configuration: json.RawMessage(`{"model":"a","x_agents_core":{"harness":"codex","harness_config":{"k":1}}}`)}
	change := &ModelProviderChange{}
	for _, tc := range []struct {
		name    string
		command UpdateCommand
		tx      *fakeUpdateTx
		wantErr error
		want    string
	}{
		{"merged revision", UpdateCommand{TenantID: "tenant", AgentID: "agent", Configuration: json.RawMessage(`{"model":"b"}`), ModelProvider: change},
			&fakeUpdateTx{current: current}, nil, `{"model":"b","x_agents_core":{"harness":"codex","harness_config":{}}}`},
		{"missing Agent", UpdateCommand{TenantID: "tenant", AgentID: "agent"}, &fakeUpdateTx{loadErr: ErrNotFound}, ErrNotFound, ""},
		{"invalid merged state is not applied", UpdateCommand{TenantID: "tenant", AgentID: "agent", Configuration: json.RawMessage(`{"x_agents_core":{"harness":"codex","harness_config":{"unknown_option":true}}}`)},
			&fakeUpdateTx{current: current}, ErrInvalidInput, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newTestService(t, &fakeStorage{withAgentUpdate: func(_ context.Context, tenantID, agentID string, decide func(UpdateTx) error) error {
				if tenantID != "tenant" || agentID != "agent" {
					t.Fatalf("locked %s/%s", tenantID, agentID)
				}
				return decide(tc.tx)
			}})
			updated, err := service.Update(context.Background(), tc.command)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Update = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if tc.tx.applied != nil {
					t.Fatal("a rejected update was applied")
				}
				return
			}
			applied := tc.tx.applied
			if applied == nil || string(applied.Configuration) != tc.want || string(updated.Configuration) != tc.want ||
				string(applied.Metadata) != `{"team":"core"}` || applied.ModelProvider != change {
				t.Fatalf("applied = %+v", applied)
			}
		})
	}
}

func TestDeletePassesThrough(t *testing.T) {
	service := newTestService(t, &fakeStorage{deleteAgent: func(_ context.Context, tenantID, agentID string) (string, error) {
		if tenantID != "tenant" {
			t.Fatalf("tenant = %s", tenantID)
		}
		return agentID, nil
	}})
	if id, err := service.Delete(context.Background(), DeleteCommand{TenantID: "tenant", AgentID: "agent"}); err != nil || id != "agent" {
		t.Fatalf("Delete = %s, %v", id, err)
	}
}
