package execution

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestDirectoryPreparationRejectsForeignBindingBeforeTransport(t *testing.T) {
	result := (&Dispatcher{}).readPreparedDirectory(t.Context(), nil,
		store.Session{ID: "session", TenantID: "tenant"},
		store.Environment{ID: "environment", SessionID: "session", TenantID: "tenant", Configuration: []byte(`{"type":"self_hosted","workspace_directory":"/workspace"}`)},
		store.ExecutionDevice{EnvironmentID: "other"}, proto.WorkspaceReadPayload{})
	if !errors.Is(result.err, ErrExecutionUnavailable) {
		t.Fatal("foreign binding reached transport", result.err)
	}
}
