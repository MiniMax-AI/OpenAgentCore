//go:build linux

package processbroker

import (
	"sync"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// terminal is the local terminal on descriptor 0 of a PTY invocation. It is
// raw while the operation runs and restored on every path. It keeps its own
// descriptor, so it outlives the stdin pump's; holding a terminal open has
// no end-of-file effect.
type terminal struct {
	fd    int
	saved unix.Termios

	mu     sync.Mutex
	raw    bool
	closed bool
}

// openTerminal returns the terminal when in and out are both terminals.
func openTerminal(in, out int) (*terminal, error) {
	t, err := unix.IoctlGetTermios(in, unix.TCGETS)
	if err != nil {
		return nil, nil
	}
	if _, err := unix.IoctlGetTermios(out, unix.TCGETS); err != nil {
		return nil, nil
	}
	fd, err := unix.FcntlInt(uintptr(in), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, err
	}
	return &terminal{fd: fd, saved: *t}, nil
}

func (t *terminal) size() sp.WindowSize {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return sp.WindowSize{}
	}
	ws, err := unix.IoctlGetWinsize(t.fd, unix.TIOCGWINSZ)
	if err != nil {
		return sp.WindowSize{}
	}
	return sp.WindowSize{Rows: ws.Row, Cols: ws.Col, XPixels: ws.Xpixel, YPixels: ws.Ypixel}
}

// spec describes the remote terminal: the local size and modes.
func (t *terminal) spec(term []byte, modes []sp.PTYMode) *sp.PTYSpec {
	return &sp.PTYSpec{Size: t.size(), Term: term, Modes: sp.ReadModes(&t.saved, modes)}
}

// makeRaw passes every byte through to the remote terminal, which does the
// line discipline.
func (t *terminal) makeRaw() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	raw := t.saved
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(t.fd, unix.TCSETS, &raw); err != nil {
		return err
	}
	t.raw = true
	return nil
}

func (t *terminal) restore() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.raw {
		unix.IoctlSetTermios(t.fd, unix.TCSETS, &t.saved)
		t.raw = false
	}
}

// close restores the terminal and closes its descriptor.
func (t *terminal) close() {
	if t == nil {
		return
	}
	t.restore()
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		unix.Close(t.fd)
	}
}
