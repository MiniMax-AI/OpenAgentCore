package localworkspace

import (
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

type WorkspaceReadResult struct {
	Data      []byte
	Truncated bool
}

type WorkspaceWriteResult struct {
	SizeBytes int64
}

var (
	ErrWorkspaceReadUnavailable = errors.New("workspace read unavailable")
	ErrWorkspaceReadBusy        = errors.New("workspace read busy")
	ErrWorkspaceReadInvalid     = errors.New("workspace read invalid")
	ErrWorkspaceReadUncertain   = errors.New("workspace read outcome uncertain")
	// ErrWorkspaceNotDirectory reports that a directory request's own path is
	// missing, a regular file or a symbolic link; the link was not followed.
	ErrWorkspaceNotDirectory = errors.New("workspace path is not a directory")
)

var (
	ErrWorkspaceWriteUnsupported = fmt.Errorf("%w: workspace write", agent.ErrUnsupportedOperation)
	ErrWorkspaceWriteUnavailable = errors.New("workspace write unavailable")
	ErrWorkspaceWriteBusy        = errors.New("workspace write busy")
	ErrWorkspaceWriteInvalid     = errors.New("workspace write invalid")
	ErrWorkspaceWriteRejected    = errors.New("workspace write rejected")
	ErrWorkspaceWriteUncertain   = errors.New("workspace write outcome uncertain")
)

// Known Files.create destination refusals. Both wrap ErrWorkspaceWriteRejected:
// nothing was installed.
var (
	ErrWorkspaceWriteDirectory = fmt.Errorf("%w: destination is a directory", ErrWorkspaceWriteRejected)
	ErrWorkspaceWriteUnsafe    = fmt.Errorf("%w: destination exists or its path is not a plain directory chain", ErrWorkspaceWriteRejected)
)
