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

func allocUID(r UIDRange) (uint32, error) {
	uids.Lock()
	defer uids.Unlock()
	for i := range r.Count {
		if id := r.First + i; !uids.used[id] {
			uids.used[id] = true
			return id, nil
		}
	}
	return 0, &Error{Kind: ErrCapacity}
}

func freeUID(id uint32) {
	uids.Lock()
	defer uids.Unlock()
	delete(uids.used, id)
}

// The entries of a Session directory.
const (
	homeEntry    = agent.ViewHomeName // the Session home, owned by the Session uid
	runEntry     = agent.ViewRunName  // the process broker's run directory
	etcEntry     = "etc"              // the /etc files
	maskEntry    = "mask"             // an empty file and an empty directory that masks present
	stagingEntry = "staging"          // sessionview's staging parent
)

func sessionsDir(stateDir string) string { return filepath.Join(stateDir, "sessions") }

// sessionDir is one Session's host directory, private to the agent host.
type sessionDir string

func (d sessionDir) entry(name ...string) string {
	return filepath.Join(append([]string{string(d)}, name...)...)
}

// createSessionDir creates the Session directory with its home, run, etc,
// mask and staging entries.
func createSessionDir(stateDir string, id sandboxwire.ID, uid uint32) (sessionDir, error) {
	parent := sessionsDir(stateDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", &Error{Kind: ErrInvalidConfig, Op: "state directory", Err: err}
	}
	d := sessionDir(filepath.Join(parent, id.String()))
	if err := os.Mkdir(string(d), 0o700); errors.Is(err, fs.ErrExist) {
		return "", &Error{Kind: ErrSessionExists, Err: err}
	} else if err != nil {
		return "", &Error{Kind: ErrInvalidConfig, Op: "session directory", Err: err}
	}
	if err := d.populate(uid); err != nil {
		os.RemoveAll(string(d))
		return "", &Error{Kind: ErrInvalidConfig, Op: "session directory", Err: err}
	}
	return d, nil
}

func (d sessionDir) populate(uid uint32) error {
	dirs := []struct {
		name string
		mode fs.FileMode
	}{{homeEntry, 0o700}, {runEntry, 0o755}, {etcEntry, 0o755}, {maskEntry, 0o755}, {filepath.Join(maskEntry, "dir"), 0o555}, {stagingEntry, 0o700}}
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

// chownHome gives the Session uid the home tree, including what the view
// Executor factory wrote there. It runs while no view is live, so no Session
// process changes the tree meanwhile, and os.Root keeps every change inside
// it.
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
