package localworkspace

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// This private transfer bound is distinct from the public inline-file limit.
const WriteMaxBytes = proto.WorkspaceWriteMaxBytes

// WriteWorkspaceFile starts only after the caller supplies the complete bounded
// body. Core must persist mutation ownership before invoking this operation.
func (b *Binding) WriteWorkspaceFile(ctx context.Context, path string, data []byte) (result dispatch.WorkspaceWriteResult, err error) {
	if len(data) > WriteMaxBytes || len(path) > 4096 || path == "." || !fs.ValidPath(path) || strings.ContainsAny(path, "\\\x00\r\n") {
		return result, dispatch.ErrWorkspaceWriteInvalid
	}
	if ctx.Err() != nil {
		return result, dispatch.ErrEnvironmentUnavailable
	}
	w := b.writer
	if !w.mu.TryLock() {
		return result, dispatch.ErrWorkspaceWriteBusy
	}
	defer w.mu.Unlock()
	if w.uncertain {
		return result, dispatch.ErrWorkspaceWriteUncertain
	}
	defer func() { w.uncertain = errors.Is(err, dispatch.ErrWorkspaceWriteUncertain) }()
	// Once admitted, finish this synchronous mutation before returning ownership.
	return b.writeNativeFile(context.WithoutCancel(ctx), path, data)
}
