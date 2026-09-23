package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type sandboxCreationRecorder struct {
	inputRecorder
	calls int
	input store.CreateSessionInput
}

func (r *sandboxCreationRecorder) CreateSession(_ context.Context, _ string, input store.CreateSessionInput) (store.Session, error) {
	r.calls++
	r.input = input
	return store.Session{}, store.ErrInvalidInput
}
func TestSandboxSelectorUsesOnlyCoreSessionExtension(t *testing.T) {
	node := uuid.NewString()
	for _, tc := range []struct {
		name, body string
		accepted   bool
	}{
		{"selector only", fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"openai_hosted"},"x_agents_core":{"sandbox_node_id":%q}}`, node), true},
		{"empty selector", `{"agent":{"model":"model"},"environment":{"type":"openai_hosted"},"x_agents_core":{"sandbox_node_id":""}}`, false},
		{"non hosted", fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"none"},"input":"hello","x_agents_core":{"sandbox_node_id":%q}}`, node), false},
		{"official environment", fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"openai_hosted","sandbox_node_id":%q}}`, node), false},
		{"agent extension", fmt.Sprintf(`{"agent":{"model":"model","x_agents_core":{"sandbox_node_id":%q}},"environment":{"type":"openai_hosted"}}`, node), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &sandboxCreationRecorder{}
			handler, _ := environmentCreationHandler(t, "codex", WithHostedEnvironments(), WithExecution(recorder))
			request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if tc.accepted {
				if recorder.calls != 1 || recorder.input.SandboxNodeID != node || strings.Contains(string(recorder.input.Configuration), "sandbox_node_id") || !strings.Contains(string(recorder.input.CreationRequest), node) {
					t.Fatal("selector routing or retry identity invalid", recorder.input, response.Body.String())
				}
			} else if recorder.calls != 0 || response.Code != 400 {
				t.Fatal("invalid selector admitted", response.Code, response.Body.String())
			}
		})
	}
}
