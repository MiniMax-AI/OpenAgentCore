package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
)

// Each placement error keeps its status, code and message, wrapped as the
// rules and their callers return it.
func TestPlacementErrorsKeepTheirResponses(t *testing.T) {
	for _, tc := range []struct {
		err           error
		status        int
		code, message string
	}{
		{placement.ErrResetAdmission, http.StatusServiceUnavailable, "sandbox_reset_in_progress", "A sandbox reset is in progress."},
		{fmt.Errorf("%w: sandbox installation does not match deployment", placement.ErrAdmissionClosed), http.StatusConflict, "environment_unavailable", "The environment is no longer available for new input."},
		{placement.ErrPublicURLUnreachable, http.StatusConflict, "sandbox_configuration_error", placement.ErrPublicURLUnreachable.Error()},
		{placement.ErrNodesPreparing, http.StatusServiceUnavailable, "sandbox_nodes_preparing", "Sandbox nodes are preparing the requested Runtime."},
		{placement.ErrNodeUnavailable, http.StatusServiceUnavailable, "runtime_node_unavailable", "The selected sandbox node is unavailable or has no capacity."},
	} {
		response := httptest.NewRecorder()
		writeOperationError(response, httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", nil), tc.err)
		var body struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != tc.status || body.Error.Code != tc.code || body.Error.Message != tc.message {
			t.Errorf("%v = %d %+v", tc.err, response.Code, body.Error)
		}
	}
}
