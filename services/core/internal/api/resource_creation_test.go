package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/google/uuid"
)

type resourceCreationStore struct{}

func (*resourceCreationStore) CreateAgent(_ context.Context, command agents.CreateCommand) (agents.Agent, error) {
	return agents.Agent{ID: uuid.NewString(), TenantID: command.TenantID, Configuration: command.Configuration, Metadata: command.Metadata}, nil
}

func (*resourceCreationStore) CreateEnvironmentTemplate(context.Context, environmenttemplates.CreateCommand) (environmenttemplates.Template, error) {
	return environmenttemplates.Template{ID: uuid.NewString(), NetworkAccess: "enabled"}, nil
}

func TestAgentAndTemplateCreationStatus(t *testing.T) {
	s := &resourceCreationStore{}
	h, _, _ := testHandler(t, func(_ *Dependencies, f *testFakes) {
		f.agents.create, f.environmentTemplates.create = s.CreateAgent, s.CreateEnvironmentTemplate
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
