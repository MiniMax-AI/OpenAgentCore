//go:build linux

package agenthost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// uids holds the Session uids in use. Only one agent host runs per kernel, so
// the set is process-wide.
var uids = struct {
	sync.Mutex
	used map[uint32]bool
}{used: map[uint32]bool{}}

// allocUID returns a uid in r that no Session uses and no process holds.
func allocUID(r UIDRange, tasks listTasks) (uint32, error) {
	held, err := heldUIDs(tasks, r)
	if err != nil {
		return 0, fmt.Errorf("%w: processes: %w", ErrInvalidConfig, err)
	}
	uids.Lock()
	defer uids.Unlock()
	for i := range r.Count {
		if id := r.First + i; !uids.used[id] && !held[id] {
			uids.used[id] = true
			return id, nil
		}
	}
	return 0, ErrCapacity
}

func freeUID(id uint32) {
	uids.Lock()
	defer uids.Unlock()
	delete(uids.used, id)
}

// The entries of a Session directory. The home holds the Harness's native
// history and persists across the Session's Executors and the agent host's
// restarts. The others are transient: each Executor creates them, and its
// Close or the next Open's sweep removes them.
const (
	homeEntry    = agent.ViewHomeName // the Session home
	etcEntry     = "etc"              // the /etc files; created first, it marks the Session in use
	maskEntry    = "mask"             // an empty file and an empty directory that masks present
	stagingEntry = "staging"          // sessionview's staging parent
)

func sessionsDir(stateDir string) string { return filepath.Join(stateDir, "sessions") }

// sessionDir is one Session's host directory, private to the agent host.
type sessionDir string

func (d sessionDir) entry(name ...string) string {
	return filepath.Join(append([]string{string(d)}, name...)...)
}

// openSessionDir prepares the Session directory for an Executor running as
// uid: it creates the directory and its home unless an earlier Executor left
// them, and the transient entries.
func openSessionDir(stateDir string, id sandboxwire.ID, uid uint32) (sessionDir, error) {
	d := sessionDir(filepath.Join(sessionsDir(stateDir), id.String()))
	if err := os.MkdirAll(d.entry(homeEntry), 0o700); err != nil {
		return "", fmt.Errorf("%w: session directory: %w", ErrInvalidConfig, err)
	}
	if err := os.Mkdir(d.entry(etcEntry), 0o755); errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%w: %w", ErrSessionExists, err)
	} else if err != nil {
		return "", fmt.Errorf("%w: session directory: %w", ErrInvalidConfig, err)
	}
	if err := d.populate(uid); err != nil {
		d.clear()
		return "", fmt.Errorf("%w: session directory: %w", ErrInvalidConfig, err)
	}
	return d, nil
}

func (d sessionDir) populate(uid uint32) error {
	dirs := []struct {
		name string
		mode fs.FileMode
	}{{maskEntry, 0o755}, {filepath.Join(maskEntry, "dir"), 0o555}, {stagingEntry, 0o700}}
	for _, e := range dirs {
		if err := os.Mkdir(d.entry(e.name), e.mode); err != nil {
			return err
		}
	}
	home := agent.ViewPrivateRoot + "/" + agent.ViewHomeName
	files := map[string]string{
		filepath.Join(maskEntry, "file"): "",
		filepath.Join(etcEntry, "passwd"): fmt.Sprintf("root:x:0:0:root:/root:/usr/sbin/nologin\noac:x:%d:%d:oac:%s:/bin/bash\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\n",
			uid, uid, home),
		filepath.Join(etcEntry, "group"):         fmt.Sprintf("root:x:0:\noac:x:%d:\nnogroup:x:65534:\n", uid),
		filepath.Join(etcEntry, "hosts"):         "127.0.0.1 localhost\n::1 localhost\n",
		filepath.Join(etcEntry, "resolv.conf"):   "",
		filepath.Join(etcEntry, "nsswitch.conf"): "passwd: files\ngroup: files\nshadow: files\nhosts: files\n",
	}
	for name, content := range files {
		if err := os.WriteFile(d.entry(name), []byte(content), 0o444); err != nil {
			return err
		}
	}
	return nil
}

// clear removes the transient entries, the one that marks the Session in use
// last.
func (d sessionDir) clear() error {
	for _, name := range []string{stagingEntry, maskEntry, etcEntry} {
		if err := os.RemoveAll(d.entry(name)); err != nil {
			return err
		}
	}
	return nil
}

// sweep removes the transient entries of every Session directory and keeps
// the homes.
func sweep(stateDir string) error {
	entries, err := os.ReadDir(sessionsDir(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := sessionDir(filepath.Join(sessionsDir(stateDir), e.Name())).clear(); err != nil {
			return err
		}
	}
	return nil
}

// RemoveHome removes the Session's directory with its home once the Session's
// processes have settled: no Executor of the Session runs, and none left its
// transient entries, or it returns ErrSessionExists. A Session without a
// directory has nothing to remove.
func (h *Host) RemoveHome(id sandboxwire.ID) error {
	d := sessionDir(filepath.Join(sessionsDir(h.cfg.StateDir), id.String()))
	switch _, err := os.Lstat(d.entry(etcEntry)); {
	case err == nil:
		return fmt.Errorf("%w: remove home", ErrSessionExists)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: remove home: %w", ErrTeardown, err)
	}
	if err := os.RemoveAll(string(d)); err != nil {
		return fmt.Errorf("%w: remove home: %w", ErrTeardown, err)
	}
	return nil
}

// chownHome gives uid the home tree, including what the view Executor
// factory and an earlier Executor's uid wrote there. It runs while no view is
// live, so no Session process changes the tree meanwhile, and os.Root keeps
// every change inside it.
func (d sessionDir) chownHome(uid uint32) error {
	root, err := os.OpenRoot(d.entry(homeEntry))
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(name string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return root.Lchown(name, int(uid), int(uid))
	})
}
