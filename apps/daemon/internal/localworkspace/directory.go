package localworkspace

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (b *Binding) ListWorkspaceDirectory(ctx context.Context, path string, limit int) (dispatch.WorkspaceDirectoryResult, error) {
	if limit < 1 || limit > proto.WorkspaceDirectoryMaxEntries || path != "" && !ValidPath(path) {
		return dispatch.WorkspaceDirectoryResult{}, dispatch.ErrWorkspaceReadInvalid
	}
	return b.listNativeDirectory(ctx, path, limit)
}
