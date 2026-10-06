//go:build linux

package agenthost

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
)

// Open starts the agent host for cfg. It takes the installation locks,
// exclusive flocks on StateDir/lock and on the ViewCgroups directory that
// the Host holds until Close. It then checks the requirements that need no
// new cgroup, recovers what an earlier agent host left, checks that
// ViewCgroups can hold a new cgroup, and only then removes the Session
// directories, as Config describes. It returns ErrInvalidConfig for a Config
// it rejects, ErrStateLocked when another agent host holds a lock,
// ErrUnsupported for a missing requirement and ErrTeardown when recovery
// failed. When it fails it has removed no Session directory.
func Open(cfg Config) (*Host, error) {
	if _, err := checkConfig(cfg); err != nil {
		return nil, err
	}
	h := &Host{cfg: cfg}
	if err := h.open(); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

func (h *Host) open() error {
	var err error
	if err := os.MkdirAll(h.cfg.StateDir, 0o700); err != nil {
		return &Error{Kind: ErrInvalidConfig, Op: "state directory", Err: err}
	}
	if h.state, err = lock(filepath.Join(h.cfg.StateDir, "lock"), os.O_RDWR|os.O_CREATE); err != nil {
		return lockError(ErrInvalidConfig, "state directory", err)
	}
	if h.views, err = lock(h.cfg.ViewCgroups, os.O_RDONLY|unix.O_DIRECTORY); err != nil {
		return lockError(ErrUnsupported, "view cgroups", err)
	}
	if err := sessionview.CheckCgroups(h.cfg.ViewCgroups); err != nil {
		return &Error{Kind: ErrUnsupported, Op: "view cgroups", Err: err}
	}
	if err := sessionview.Probe(); err != nil {
		return &Error{Kind: ErrUnsupported, Op: "views", Err: err}
	}
	return sweep(h.cfg)
}

// lock opens path with flag and takes an exclusive flock on it, which lasts
// until the file is closed.
func lock(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// lockError reports a lock that another agent host holds as ErrStateLocked
// and any other failure as kind.
func lockError(kind error, op string, err error) error {
	if errors.Is(err, unix.EWOULDBLOCK) {
		kind = ErrStateLocked
	}
	return &Error{Kind: kind, Op: op, Err: err}
}

// sweep ends every view cgroup an earlier agent host left, with all its
// processes, checks that ViewCgroups can hold a new cgroup, which leftover
// cgroups may prevent until they are ended, and only then removes the
// Session directories. When a step fails it removes nothing.
func sweep(cfg Config) error {
	if err := sessionview.Recover(cfg.ViewCgroups); err != nil {
		return &Error{Kind: ErrTeardown, Op: "recover views", Err: err}
	}
	if err := sessionview.ProbeCgroups(cfg.ViewCgroups); err != nil {
		return &Error{Kind: ErrUnsupported, Op: "view cgroups", Err: err}
	}
	if err := os.RemoveAll(sessionsDir(cfg.StateDir)); err != nil {
		return &Error{Kind: ErrTeardown, Op: "remove session directories", Err: err}
	}
	return nil
}
