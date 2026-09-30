package localworkspace

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// This private transfer bound is distinct from the public inline-file limit.
const WriteMaxBytes = proto.WorkspaceWriteMaxBytes

var _ agent.WorkspaceWriter = (*Binding)(nil)

func (b *Binding) AcceptsFileWrite(environment, session string) bool {
	return b != nil && b.writer != nil && b.environment == environment && b.stateKey == "agents-api-"+session
}

// WriteWorkspaceFile starts only after the caller supplies the complete bounded
// body. Core must persist mutation ownership before invoking this operation.
func (b *Binding) WriteWorkspaceFile(ctx context.Context, path string, data []byte) (result agent.WorkspaceWriteResult, err error) {
	if b == nil || b.writer == nil {
		return result, agent.ErrWorkspaceWriteUnsupported
	}
	if len(data) > WriteMaxBytes || len(path) > 4096 || path == "." || !fs.ValidPath(path) || strings.ContainsAny(path, "\\\x00\r\n") {
		return result, agent.ErrWorkspaceWriteInvalid
	}
	if ctx.Err() != nil {
		return result, agent.ErrWorkspaceWriteUnavailable
	}
	w := b.writer
	if !w.mu.TryLock() {
		return result, agent.ErrWorkspaceWriteBusy
	}
	defer w.mu.Unlock()
	if w.uncertain {
		return result, agent.ErrWorkspaceWriteUncertain
	}
	defer func() { w.uncertain = errors.Is(err, agent.ErrWorkspaceWriteUncertain) }()
	// Once admitted, finish this synchronous mutation before returning ownership.
	return b.writeNativeFile(context.WithoutCancel(ctx), path, data)
}
