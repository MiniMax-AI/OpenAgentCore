package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSandboxNodePatchRejectsNullAndUnknownFields(t *testing.T) {
	project, _ := NewAuthenticator([]APIKey{callerBinding()})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`null`, `[]`, `{"name":null}`, `{"max_active":null}`, `{"max_retained":null}`, `{"admission_state":null}`, `{"expected_config_revision":null}`, `{"max_active":1.5}`, `{"Name":"bad"}`, `{"provider":"docker"}`, `{"max_active":"2"}`} {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/core/v1/sandbox/nodes/node", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer administrator")
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}
func TestSandboxNodeConfigurationConflictIsActionable(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/core/v1/sandbox/nodes/node", nil)
	writeStoreError(res, req, store.ErrRuntimeNodeConfigurationConflict)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "runtime_node_configuration_conflict") || !strings.Contains(res.Body.String(), "Refresh") {
		t.Fatal(res.Code, res.Body.String())
	}
}
