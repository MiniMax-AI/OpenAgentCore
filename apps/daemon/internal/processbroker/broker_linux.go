//go:build linux

package processbroker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
)

// Broker serves one Session's shim invocations.
type Broker struct {
	cfg   Config
	log   *slog.Logger
	dir   int // the run directory
	ln    *net.UnixListener
	link  *link
	terms terminals

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// Start listens at processshim.SocketName in cfg.RunDir and serves until
// Close.
func Start(cfg Config) (*Broker, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	dir, err := openRunDir(cfg.RunDir)
	if err != nil {
		return nil, err
	}
	ln, err := listen(dir, cfg.UID)
	if err != nil {
		unix.Close(dir)
		return nil, fmt.Errorf("processbroker: listen: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{cfg: cfg, log: log, dir: dir, ln: ln, link: newLink(cfg.Dial, log), ctx: ctx, cancel: cancel}
	b.terms.init()
	b.wg.Add(1)
	go b.accept()
	return b, nil
}

// openRunDir opens the run directory without following a symlink and checks
// that only the broker's user can change it, so the socket's name stays the
// broker's.
func openRunDir(path string) (int, error) {
	dir, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("%w: run directory: %w", ErrInvalidConfig, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(dir, &st); err != nil {
		unix.Close(dir)
		return -1, fmt.Errorf("processbroker: run directory: %w", err)
	}
	if euid := unix.Geteuid(); int(st.Uid) != euid || st.Mode&0o022 != 0 {
		unix.Close(dir)
		return -1, fmt.Errorf("%w: run directory %s has owner %d and mode %#o; it must be owned by uid %d and writable by no one else", ErrInvalidConfig, path, st.Uid, st.Mode&0o7777, euid)
	}
	return dir, nil
}

// listen binds the socket in dir and gives it to uid. Every name operation
// is relative to dir, which no one else can change.
func listen(dir, uid int) (*net.UnixListener, error) {
	const name = processshim.SocketName
	var st unix.Stat_t
	if err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW); err == nil && st.Mode&unix.S_IFMT == unix.S_IFSOCK {
		unix.Unlinkat(dir, name, 0) // a previous broker's
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: fmt.Sprintf("/proc/self/fd/%d/%s", dir, name), Net: "unix"})
	if err != nil {
		return nil, err
	}
	ln.SetUnlinkOnClose(false)
	if err := errors.Join(unix.Fchmodat(dir, name, 0o600, 0), unix.Fchownat(dir, name, uid, -1, unix.AT_SYMLINK_NOFOLLOW)); err != nil {
		ln.Close()
		unix.Unlinkat(dir, name, 0)
		return nil, err
	}
	return ln, nil
}

// Close stops accepting, ends every invocation and waits for them. A shim
// still waiting gets 255. Remote operations are left to the Session.
func (b *Broker) Close() error {
	b.closeOnce.Do(func() {
		b.cancel()
		b.closeErr = b.ln.Close()
		unix.Unlinkat(b.dir, processshim.SocketName, 0)
		unix.Close(b.dir)
		b.wg.Wait()
		b.link.close()
	})
	return b.closeErr
}

func (b *Broker) accept() {
	defer b.wg.Done()
	for {
		c, err := b.ln.AcceptUnix()
		if err != nil {
			if b.ctx.Err() == nil {
				b.log.Error("process broker stopped accepting", "error", err)
			}
			return
		}
		// Until the invocation runs, Close ends the connection, which ends
		// any read or write of the handshake.
		unwatch := context.AfterFunc(b.ctx, func() { c.Close() })
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			b.serve(c, unwatch)
		}()
	}
}

func peerCred(c *net.UnixConn) (unix.Ucred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return unix.Ucred{}, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return unix.Ucred{}, err
	}
	if cerr != nil {
		return unix.Ucred{}, cerr
	}
	return *cred, nil
}
