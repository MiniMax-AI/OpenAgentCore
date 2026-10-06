package worldfs

import (
	"context"
	"errors"
	"io"
)

// Dial opens a new File stream to the attachment's service. It returns once ctx ends; ctx bounds the open, not the stream.
type Dial func(ctx context.Context) (io.ReadWriteCloser, error)

// Error kinds. Every error the package returns, and every reason [World.Err] reports, matches one of them with errors.Is.
var (
	ErrUnsupported     = errors.New("worldfs: unsupported platform")
	ErrIncompatible    = errors.New("worldfs: file service profile not supported")
	ErrConnect         = errors.New("worldfs: cannot reach the file service")
	ErrMountpoint      = errors.New("worldfs: mountpoint cannot be presented")
	ErrInstanceChanged = errors.New("worldfs: file service instance changed")
	ErrAttachmentLost  = errors.New("worldfs: attachment ended")
	ErrTopologyChanged = errors.New("worldfs: pinned topology changed")
	// ErrAttachmentDirty is a failed Serve whose cleanup Detach could not be sent or answered, so it cannot show the attachment holds nothing: the owner of the Link attachment must end it.
	ErrAttachmentDirty = errors.New("worldfs: the attachment may still hold state")
)
