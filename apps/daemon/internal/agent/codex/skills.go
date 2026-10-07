package codex

import (
	"context"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func effectiveAgentStateKey(req proto.PromptRequestPayload) string {
	if strings.TrimSpace(req.AgentStateKey) != "" {
		return req.AgentStateKey
	}
	if id := strings.TrimSpace(req.ConversationID); id != "" {
		return "_legacy_conversation/" + id + "/codex"
	}
	if id := strings.TrimSpace(req.RunID); id != "" {
		return "_legacy_run/" + id + "/codex"
	}
	return ""
}

func setSkillExtraRoots(ctx context.Context, rpc *JSONRPCClient, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	_, err := rpc.Request(ctx, "skills/extraRoots/set", SkillsExtraRootsSetParams{ExtraRoots: roots})
	return err
}
