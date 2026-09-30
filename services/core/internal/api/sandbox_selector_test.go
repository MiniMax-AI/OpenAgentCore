package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type sandboxCreationRecorder struct {
	inputRecorder
	calls int
}

func (r *sandboxCreationRecorder) CreateSession(context.Context, string, store.CreateSessionInput) (store.Session, error) {
	r.calls++
	return store.Session{}, store.ErrInvalidInput
}

// Placement is automatic. A node selector is an unknown member wherever it appears.
func TestSessionCreationRejectsSandboxNodeSelector(t *testing.T) {
	node := uuid.NewString()
	for _, body := range []string{
		fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"openai_hosted"},"x_agents_core":{"sandbox_node_id":%q}}`, node),
		fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"openai_hosted","sandbox_node_id":%q}}`, node),
		fmt.Sprintf(`{"agent":{"model":"model","x_agents_core":{"sandbox_node_id":%q}},"environment":{"type":"openai_hosted"}}`, node),
	} {
		recorder := &sandboxCreationRecorder{}
		handler, _ := environmentCreationHandler(t, "codex", WithHostedEnvironments(), WithExecution(recorder))
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer key")
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if recorder.calls != 0 || response.Code != http.StatusBadRequest {
			t.Fatal("node selector admitted", body, response.Code, response.Body.String())
		}
	}
}
