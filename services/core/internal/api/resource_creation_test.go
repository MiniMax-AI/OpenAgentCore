package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type resourceCreationStore struct{}

func (*resourceCreationStore) CreateAgent(_ context.Context, tenant string, input store.CreateAgentInput) (store.SavedAgent, error) {
	return store.SavedAgent{ID: uuid.NewString(), TenantID: tenant, Configuration: input.Configuration, Metadata: input.Metadata}, nil
}

func (*resourceCreationStore) CreateEnvironmentTemplate(context.Context, string, store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error) {
	return store.EnvironmentTemplate{ID: uuid.NewString(), NetworkAccess: "enabled"}, nil
}

func TestAgentAndTemplateCreationStatus(t *testing.T) {
	s := &resourceCreationStore{}
	h, _, _ := testHandler(t, func(_ *Dependencies, f *testFakes) {
		f.agents.createAgent, f.environmentTemplates.createEnvironmentTemplate = s.CreateAgent, s.CreateEnvironmentTemplate
	})
	for _, tc := range []struct{ path, body, object string }{
		{"/v1/agents", `{"model":"resource-model"}`, "agent"},
		{"/v1/agents/environments/templates", `{}`, "agent.environment.template"},
	} {
		t.Run(tc.object, func(t *testing.T) {
			w := credentialRequest(h, http.MethodPost, tc.path, tc.body)
			var body struct{ ID, Object string }
			if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.ID == "" || body.Object != tc.object {
				t.Fatal("creation did not return the created resource", w.Code, w.Body)
			}
		})
	}
}
