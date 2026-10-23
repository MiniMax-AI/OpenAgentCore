package localworkspace

import (
	"context"
	"io/fs"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (b *Binding) ListWorkspaceDirectory(ctx context.Context, path string, limit int) (*proto.WorkspaceDirectoryResult, error) {
	if limit < 1 || limit > proto.WorkspaceDirectoryMaxEntries || len(path) > 4096 || strings.ContainsAny(path, "\\\x00\r\n") || (path != "" && (path == "." || !fs.ValidPath(path))) {
		return nil, ErrWorkspaceReadInvalid
	}
	return b.listNativeDirectory(ctx, path, limit)
}
