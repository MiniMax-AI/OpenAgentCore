//go:build linux

package processservice

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// delegatedCgroup advertises the stronger scope only after exercising the
// Provider's delegation. A missing or read-only delegation leaves POSIX scope
// available; callers still select the scope explicitly from Describe.
func delegatedCgroup() string {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(raw)) {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "0::")
		if !ok || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			continue
		}
		parent := filepath.Join("/sys/fs/cgroup", path)
		var stat unix.Statfs_t
		if unix.Statfs(parent, &stat) != nil || stat.Type != unix.CGROUP2_SUPER_MAGIC {
			return ""
		}
		// This also rejects a mount whose root does not match the cgroup namespace.
		procs, err := os.ReadFile(filepath.Join(parent, "cgroup.procs"))
		if err != nil || !slices.Contains(strings.Fields(string(procs)), strconv.Itoa(os.Getpid())) {
			return ""
		}
		// Migrating a child requires write access to the common ancestor's
		// cgroup.procs as well as the destination. Opening changes no membership.
		migration, err := os.OpenFile(filepath.Join(parent, "cgroup.procs"), os.O_WRONLY, 0)
		if err != nil {
			return ""
		}
		migration.Close()
		g, err := newProcessCgroup(parent)
		if err != nil {
			return ""
		}
		kind, err := g.root.ReadFile("cgroup.type")
		if err == nil && strings.TrimSpace(string(kind)) != "domain" {
			err = errors.New("operation cgroup is not a domain")
		}
		if err == nil {
			var destination *os.File
			destination, err = g.root.OpenFile("cgroup.procs", os.O_WRONLY, 0)
			if err == nil {
				destination.Close()
				// Probe the control file directly: Signal on an empty scope
				// must instead report NotRunning.
				err = g.root.WriteFile("cgroup.kill", []byte("1"), 0)
			}
		}
		_, observation := g.observe()
		// No process was ever placed in this exclusive probe group.
		g.closed = true
		cleanup := g.cleanup()
		if err == nil && observation == nil && cleanup == nil {
			return parent
		}
		return ""
	}
	return ""
}

// processCgroup owns one operation's cgroup. It is containment for process
// lifetime, not isolation from other processes running as the same account.
// Its descriptor pins the group while a signal races scope removal.
type processCgroup struct {
	mu     sync.Mutex
	root   *os.Root
	path   string
	closed bool
}

func newProcessCgroup(parent string) (*processCgroup, error) {
	path, err := os.MkdirTemp(parent, "oac-process-")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &processCgroup{root: root, path: path}, nil
}

func (g *processCgroup) place(pid int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.root.WriteFile("cgroup.procs", []byte(strconv.Itoa(pid)), 0)
}

func (g *processCgroup) signal(sig unix.Signal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return notRunning("the cgroup is empty")
	}
	if sig == unix.SIGKILL {
		live, observation := g.observeLocked()
		if observation == nil && live == 0 {
			return notRunning("the cgroup is empty")
		}
		if err := g.root.WriteFile("cgroup.kill", []byte("1"), 0); err != nil {
			return sp.Fail(sp.CodeIO, sandboxwire.EffectPossible, "kill cgroup: %v", err)
		}
		if observation != nil {
			return sp.Fail(sp.CodeIO, sandboxwire.EffectPossible, "kill cgroup with unknown membership: %v", observation)
		}
		return nil
	}
	// Membership comes from this operation's cgroup, never a process-tree scan.
	// A pidfd plus a second membership read excludes a PID reused outside it.
	var errs []error
	sent := 0
	err := fs.WalkDir(g.root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		procs, err := g.root.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return err
		}
		for _, word := range strings.Fields(string(procs)) {
			pid, err := strconv.Atoi(word)
			if err != nil {
				return err
			}
			fd, err := unix.PidfdOpen(pid, 0)
			if errors.Is(err, unix.ESRCH) {
				continue
			}
			if err != nil {
				return err
			}
			current, err := g.root.ReadFile(filepath.Join(path, "cgroup.procs"))
			if err == nil && slices.Contains(strings.Fields(string(current)), word) {
				err = unix.PidfdSendSignal(fd, sig, nil, 0)
				if err == nil {
					sent++
				}
			}
			unix.Close(fd)
			if err != nil && !errors.Is(err, unix.ESRCH) {
				errs = append(errs, err)
			}
		}
		return nil
	})
	return signalResult(sent, err, errors.Join(errs...), "the cgroup is empty")
}

// observe reports closed only after the kernel reports the whole subtree
// empty. An observation failure keeps the operation unknown for the next poll.
func (g *processCgroup) observe() (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.observeLocked()
}

func (g *processCgroup) observeLocked() (int, error) {
	if g.closed {
		return 0, nil
	}
	raw, err := g.root.ReadFile("cgroup.events")
	if err != nil {
		return 0, err
	}
	empty := false
	for line := range strings.Lines(string(raw)) {
		switch strings.TrimSpace(line) {
		case "populated 1":
			return 1, nil
		case "populated 0":
			empty = true
		}
	}
	if !empty {
		return 0, fmt.Errorf("cgroup.events has no populated state")
	}
	g.closed = true
	return 0, nil
}

// cleanup removes only an already-empty owned subtree. A removal failure is
// distinct from an unknown process scope; Shutdown makes one more attempt.
func (g *processCgroup) cleanup() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.root == nil {
		return nil
	}
	if !g.closed {
		return errors.New("the process cgroup is not confirmed empty")
	}

	var dirs []string
	err := fs.WalkDir(g.root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != "." {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range slices.Backward(dirs) {
		if err := g.root.Remove(path); err != nil {
			return err
		}
	}
	if err := os.Remove(g.path); err != nil {
		return err
	}
	err = g.root.Close()
	g.root = nil
	return err
}
