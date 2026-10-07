//go:build linux

package agenthost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
)

// Open starts the agent host for cfg, as the package documentation
// describes.
func Open(cfg Config) (_ *Host, err error) {
	if _, err := checkConfig(cfg); err != nil {
		return nil, err
	}
	// Recovery's check that the agent host runs outside ViewCgroups does
	// not follow a symlink.
	if p, err := filepath.EvalSymlinks(cfg.ViewCgroups); err != nil || p != cfg.ViewCgroups {
		return nil, invalidConfig("view cgroups %q is not the canonical path of a directory", cfg.ViewCgroups)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: state directory: %w", ErrInvalidConfig, err)
	}
	h := &Host{cfg: cfg}
	defer func() {
		if err != nil {
			h.Close()
		}
	}()
	if h.state, err = lock(filepath.Join(cfg.StateDir, "lock"), os.O_RDWR|os.O_CREATE); err != nil {
		return nil, err
	}
	if h.views, err = lock(cfg.ViewCgroups, os.O_RDONLY|unix.O_DIRECTORY); err != nil {
		return nil, err
	}
	if err := sessionview.Recover(cfg.ViewCgroups); err != nil {
		kind := ErrUnsupported
		if errors.Is(err, sessionview.ErrCleanup) {
			kind = ErrTeardown
		}
		return nil, fmt.Errorf("%w: view cgroups: %w", kind, err)
	}
	if err := sessionview.Probe(); err != nil {
		return nil, fmt.Errorf("%w: views: %w", ErrUnsupported, err)
	}
	if err := os.RemoveAll(sessionsDir(cfg.StateDir)); err != nil {
		return nil, fmt.Errorf("%w: remove session directories: %w", ErrTeardown, err)
	}
	return h, nil
}

// lock opens path with flag and takes an exclusive flock on it, which lasts
// until the file is closed.
func lock(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: lock: %w", ErrInvalidConfig, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		kind := ErrInvalidConfig
		if errors.Is(err, unix.EWOULDBLOCK) {
			kind = ErrStateLocked
		}
		return nil, fmt.Errorf("%w: lock %s: %w", kind, path, err)
	}
	return f, nil
}
