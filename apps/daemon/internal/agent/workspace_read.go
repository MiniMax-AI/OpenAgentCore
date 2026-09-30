package agent

import (
	"errors"
	"fmt"
)

type WorkspaceReadResult struct {
	Data      []byte
	Truncated bool
}

var (
	ErrWorkspaceReadUnsupported = fmt.Errorf("%w: workspace read", ErrUnsupportedOperation)
	ErrWorkspaceReadUnavailable = errors.New("workspace read unavailable")
	ErrWorkspaceReadBusy        = errors.New("workspace read busy")
	ErrWorkspaceReadInvalid     = errors.New("workspace read invalid")
	ErrWorkspaceReadUncertain   = errors.New("workspace read outcome uncertain")
	// ErrWorkspaceNotDirectory reports that a directory request's own path is
	// missing, a regular file or a symbolic link; the link was not followed.
	ErrWorkspaceNotDirectory = errors.New("workspace path is not a directory")
)
