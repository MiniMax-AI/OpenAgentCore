//go:build linux

package processbroker

import (
	"sync"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// terminals coordinates the invocations that share a local terminal: the
// first one saves the terminal's settings, each one runs it raw from those
// settings, and the last one to leave restores them.
type terminals struct {
	mu sync.Mutex
	m  map[termKey]*sharedTerminal
}

// termKey identifies a terminal device.
type termKey struct{ dev, rdev uint64 }

type sharedTerminal struct {
	saved unix.Termios // fixed once created
	users int
	raw   bool
}

func (ts *terminals) init() { ts.m = map[termKey]*sharedTerminal{} }

// terminal is the local terminal on descriptor 0 of a PTY invocation. It is
// raw while the operation runs and restored on every path. It keeps its own
// descriptor, so it outlives the stdin pump's; holding a terminal open has
// no end-of-file effect.
type terminal struct {
	fd     int
	reg    *terminals
	key    termKey
	shared *sharedTerminal

	mu     sync.Mutex
	left   bool // the invocation no longer uses the terminal
	closed bool
}

// openTerminal returns the terminal when in and out are both terminals, and
// counts the invocation as one of its users.
func openTerminal(reg *terminals, in, out int) (*terminal, error) {
	cur, err := unix.IoctlGetTermios(in, unix.TCGETS)
	if err != nil {
		return nil, nil
	}
	if _, err := unix.IoctlGetTermios(out, unix.TCGETS); err != nil {
		return nil, nil
	}
	var st unix.Stat_t
	if err := unix.Fstat(in, &st); err != nil {
		return nil, err
	}
	fd, err := unix.FcntlInt(uintptr(in), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, err
	}
	key := termKey{dev: st.Dev, rdev: st.Rdev}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	s := reg.m[key]
	if s == nil {
		s = &sharedTerminal{saved: *cur}
		reg.m[key] = s
	}
	s.users++
	return &terminal{fd: fd, reg: reg, key: key, shared: s}, nil
}

// saved is the terminal's mode before any invocation made it raw.
func (t *terminal) saved() *unix.Termios { return &t.shared.saved }

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

// makeRaw passes every byte through to the remote terminal, which does the
// line discipline.
func (t *terminal) makeRaw() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.left {
		return nil
	}
	raw := t.shared.saved
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	t.reg.mu.Lock()
	defer t.reg.mu.Unlock()
	if err := unix.IoctlSetTermios(t.fd, unix.TCSETS, &raw); err != nil {
		return err
	}
	t.shared.raw = true
	return nil
}

// restore ends the invocation's use of the terminal. The last user restores
// the saved mode.
func (t *terminal) restore() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.left {
		return
	}
	t.left = true
	t.reg.mu.Lock()
	defer t.reg.mu.Unlock()
	s := t.shared
	if s.users--; s.users > 0 {
		return
	}
	delete(t.reg.m, t.key)
	if s.raw {
		unix.IoctlSetTermios(t.fd, unix.TCSETS, &s.saved)
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
