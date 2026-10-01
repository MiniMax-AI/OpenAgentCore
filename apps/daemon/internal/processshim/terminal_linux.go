//go:build linux

package processshim

import (
	"sync"

	"golang.org/x/sys/unix"
)

// terminals coordinates the invocations that share a terminal: the first
// one saves the terminal's mode, each one runs it raw from that mode, and
// the last one to leave restores it. Saving and restoring both happen under
// mu, so an invocation that arrives while the last one leaves saves the
// restored mode, never the raw one.
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

// terminal is the terminal on descriptor 0 of a PTY invocation. It is raw
// while the program runs and restored on every path. It keeps its own
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
	if !isTerminal(in) || !isTerminal(out) {
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
		cur, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		if err != nil {
			unix.Close(fd)
			return nil, err
		}
		s = &sharedTerminal{saved: *cur}
		if reg.m == nil {
			reg.m = map[termKey]*sharedTerminal{}
		}
		reg.m[key] = s
	}
	s.users++
	return &terminal{fd: fd, reg: reg, key: key, shared: s}, nil
}

func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

// mode is the terminal's saved mode and current size.
func (t *terminal) mode() *Terminal {
	s := t.shared.saved
	return &Terminal{
		Size:  t.size(),
		Iflag: s.Iflag, Oflag: s.Oflag, Cflag: s.Cflag, Lflag: s.Lflag,
		Cc: append([]byte(nil), s.Cc[:]...),
	}
}

func (t *terminal) size() WindowSize {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return WindowSize{}
	}
	ws, err := unix.IoctlGetWinsize(t.fd, unix.TIOCGWINSZ)
	if err != nil {
		return WindowSize{}
	}
	return WindowSize{Rows: ws.Row, Cols: ws.Col, XPixels: ws.Xpixel, YPixels: ws.Ypixel}
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
