package agent

import (
	"errors"
	"fmt"
)

type WorkspaceWriteResult struct {
	SizeBytes int64
}

var (
	ErrWorkspaceWriteUnsupported = fmt.Errorf("%w: workspace write", ErrUnsupportedOperation)
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
