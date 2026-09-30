package codex

import (
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Runtime execution uses the current user; isolation belongs to its outer host.
func runtimePermissionProfile(req proto.PromptRequestPayload) (string, error) {
	if req.LocalEnvironment != nil && (req.DisableExecutionEnvironment || req.WorkspaceReadOnly || req.LocalEnvironment.NetworkAccess != "enabled" || len(req.LocalEnvironment.AllowedDomains) != 0) {
		return "", fmt.Errorf("codex: Runtime execution requires unrestricted host access")
	}
	return "", nil
}
