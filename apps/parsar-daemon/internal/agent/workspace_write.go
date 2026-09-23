package agent

import (
	"context"
	"errors"
	"fmt"
)

type WorkspaceWriteResult struct {
	SizeBytes int64
}

// WorkspaceWriter confirms a native commit on an already authorized prepared owner.
type WorkspaceWriter interface {
	WriteWorkspaceFile(context.Context, string, []byte) (WorkspaceWriteResult, error)
}

var (
	ErrWorkspaceWriteUnsupported = errors.New("workspace write unsupported")
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
