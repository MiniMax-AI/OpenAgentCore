package localworkspace

import (
	"context"
	"io/fs"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (b *Binding) ListWorkspaceDirectory(ctx context.Context, path string, limit int) (agent.WorkspaceDirectoryResult, error) {
	if limit < 1 || limit > proto.WorkspaceDirectoryMaxEntries || len(path) > 4096 || strings.ContainsAny(path, "\\\x00\r\n") || (path != "" && (path == "." || !fs.ValidPath(path))) {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadInvalid
	}
	return b.listNativeDirectory(ctx, path, limit)
}
