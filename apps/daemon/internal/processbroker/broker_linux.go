//go:build linux

package processbroker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
)

// Broker serves one Session's shim invocations.
type Broker struct {
	cfg  Config
	log  *slog.Logger
	ln   *net.UnixListener
	link *link

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	sock := filepath.Join(cfg.RunDir, processshim.SocketName)
	if fi, err := os.Lstat(sock); err == nil && fi.Mode().Type() == os.ModeSocket {
		os.Remove(sock) // a previous broker's
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("processbroker: listen: %w", err)
	}
	if err := errors.Join(os.Chown(sock, cfg.UID, -1), os.Chmod(sock, 0o600)); err != nil {
		ln.Close()
		return nil, fmt.Errorf("processbroker: socket owner: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{cfg: cfg, log: log, ln: ln, link: newLink(cfg.Dial, log), ctx: ctx, cancel: cancel}
	b.wg.Add(1)
	go b.accept()
	return b, nil
}

// Close stops accepting, ends every invocation and waits for them. A shim
// still waiting gets 255. Remote operations are left to the Session.
func (b *Broker) Close() error {
	b.cancel()
	err := b.ln.Close()
	b.wg.Wait()
	b.link.close()
	return err
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
