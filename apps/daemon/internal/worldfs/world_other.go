//go:build !linux

package worldfs

import (
	"context"
	"fmt"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
)

// World serves one Session's world to one view. It is Linux-only.
type World struct{}

// New returns a world that fails to serve on this platform.
func New(Dial) *World { return &World{} }

// Serve reports ErrUnsupported.
func (w *World) Serve(context.Context, *os.File, sessionview.WorldMount) (sessionview.WorldServer, sessionview.Presentation, error) {
	return nil, sessionview.Presentation{}, fmt.Errorf("%w: serve", ErrUnsupported)
}

// Stop does nothing: the world never served.
func (w *World) Stop() error { return nil }

// Lost is never closed.
func (w *World) Lost() <-chan struct{} { return nil }

// Err is always nil.
func (w *World) Err() error { return nil }
