package codex

import (
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Runtime execution uses the current user; isolation belongs to its outer host.
func validatePermissionProfile(req proto.PromptRequestPayload) error {
	if req.LocalEnvironment != nil && req.WorkspaceReadOnly {
		return fmt.Errorf("codex: Runtime execution requires a writable workspace")
	}
	return nil
}
