//go:build linux

package agenthost

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
)

// Open starts the agent host for cfg. It takes the installation lock, an
// exclusive flock on StateDir/lock that the Host holds until Close, then
// checks the host's requirements and recovers what an earlier agent host
// left, as Config describes. It returns ErrInvalidConfig for a Config it
// rejects, ErrStateLocked when another agent host holds the lock,
// ErrUnsupported for a missing requirement and ErrTeardown when recovery
// failed.
func Open(cfg Config) (*Host, error) {
	if _, err := checkConfig(cfg); err != nil {
		return nil, err
	}
	lock, err := lockState(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	if err := sessionview.CheckCgroups(cfg.ViewCgroups); err != nil {
		lock.Close()
		return nil, &Error{Kind: ErrUnsupported, Op: "view cgroups", Err: err}
	}
	if err := sessionview.Probe(); err != nil {
		lock.Close()
		return nil, &Error{Kind: ErrUnsupported, Op: "views", Err: err}
	}
	if err := sweep(cfg); err != nil {
		lock.Close()
		return nil, err
	}
	return &Host{cfg: cfg, lock: lock}, nil
}

// lockState takes the installation lock of stateDir.
func lockState(stateDir string) (*os.File, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, &Error{Kind: ErrInvalidConfig, Op: "state directory", Err: err}
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, &Error{Kind: ErrInvalidConfig, Op: "state directory", Err: err}
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, &Error{Kind: ErrStateLocked, Err: err}
		}
		return nil, &Error{Kind: ErrInvalidConfig, Op: "lock", Err: err}
	}
	return f, nil
}

// sweep ends every view cgroup an earlier agent host left, with all its
// processes, and only then removes the Session directories. When the
// cgroups are not all ended it removes nothing.
func sweep(cfg Config) error {
	if err := sessionview.Recover(cfg.ViewCgroups); err != nil {
		return &Error{Kind: ErrTeardown, Op: "recover views", Err: err}
	}
	if err := os.RemoveAll(sessionsDir(cfg.StateDir)); err != nil {
		return &Error{Kind: ErrTeardown, Op: "remove session directories", Err: err}
	}
	return nil
}
