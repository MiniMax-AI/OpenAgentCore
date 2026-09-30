package execution

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestDirectoryPreparationRejectsForeignBindingBeforeTransport(t *testing.T) {
	result := (&Dispatcher{}).readPreparedDirectory(t.Context(), nil,
		sessions.Session{ID: "session", TenantID: "tenant"},
		sessions.Environment{ID: "environment", SessionID: "session", TenantID: "tenant", Configuration: []byte(`{"type":"self_hosted","workspace_directory":"/workspace"}`)},
		sessions.ExecutionDevice{EnvironmentID: "other"}, proto.WorkspaceReadPayload{})
	if !errors.Is(result.err, ErrExecutionUnavailable) {
		t.Fatal("foreign binding reached transport", result.err)
	}
}
