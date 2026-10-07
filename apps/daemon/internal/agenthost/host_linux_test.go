//go:build linux

package agenthost

import (
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// TestOpenReclaimsNothingWithoutViewCgroups checks that Open rejects a
// ViewCgroups that is not a cgroup v2 directory, or that is a symlink, here to
// a cgroup tree this process runs in, and keeps every transient entry.
func TestOpenReclaimsNothingWithoutViewCgroups(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink("/sys/fs/cgroup", link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		dir  string
		want []error
	}{
		{t.TempDir(), []error{ErrUnsupported, sessionview.ErrCgroup}},
		{link, []error{ErrInvalidConfig}},
	} {
		cfg := newConfig(t, agent.NewRegistry(), testCA())
		cfg.ViewCgroups = c.dir
		left := plantSession(t, cfg, sandboxwire.NewID())
		_, err := Open(cfg)
		for _, want := range c.want {
			if !errors.Is(err, want) {
				t.Errorf("Open with ViewCgroups %s = %v, want %v", c.dir, err, want)
			}
		}
		if _, err := os.Stat(left.entry(etcEntry)); err != nil {
			t.Errorf("Open with ViewCgroups %s reclaimed a transient entry: %v", c.dir, err)
		}
	}
}

// TestOpenReclaimsNothingUnrecovered checks that a view cgroup that Open
// cannot remove, here one that holds a cgroup of its own, keeps every
// transient entry.
func TestOpenReclaimsNothingUnrecovered(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see view_linux_test.go", gateEnv)
	}
	cfg := newConfig(t, agent.NewRegistry(), testCA())
	cfg.ViewCgroups = sessionviewtest.CgroupParent(t)
	stuck := filepath.Join(cfg.ViewCgroups, "view-stuck")
	if err := os.MkdirAll(filepath.Join(stuck, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	left := plantSession(t, cfg, sandboxwire.NewID())
	_, err := Open(cfg)
	os.Remove(filepath.Join(stuck, "nested"))
	os.Remove(stuck)
	if !errors.Is(err, ErrTeardown) || !errors.Is(err, sessionview.ErrCleanup) {
		t.Errorf("Open = %v, want ErrTeardown with sessionview.ErrCleanup", err)
	}
	if _, err := os.Stat(left.entry(etcEntry)); err != nil {
		t.Errorf("Open reclaimed a transient entry before recovery: %v", err)
	}
}

// TestOpenRecoversWhatAnEarlierAgentHostLeft plants what a crashed agent host
// leaves: a Session directory whose view's processes keep forking, a view
// cgroup that no Session directory names, and an empty view cgroup, which
// together exhaust the descendant limit of ViewCgroups. Open ends every one
// of their processes before it removes the transient entries, keeps the
// home, and spares a process outside its view cgroups that has a Session
// uid, the launcher's argv and PID 1 of its own namespace. While it runs, no
// other agent host opens the same StateDir or the same ViewCgroups.
func TestOpenRecoversWhatAnEarlierAgentHostLeft(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see view_linux_test.go", gateEnv)
	}
	cfg := newConfig(t, agent.NewRegistry(), testCA())
	cfg.ViewCgroups = sessionviewtest.CgroupParent(t)
	planted := plantSession(t, cfg, sandboxwire.NewID())
	crashed := filepath.Join(cfg.ViewCgroups, "view-crashed")
	forking := startInCgroup(t, crashed, cfg.UIDs.First, "/bin/sh", "-c", "while :; do sleep 60 & sleep 0.01; done")
	until(t, "the crashed view's processes to fork", func() bool {
		procs, err := os.ReadFile(filepath.Join(crashed, "cgroup.procs"))
		return err == nil && strings.Count(string(procs), "\n") >= 3
	})
	orphan := startInCgroup(t, filepath.Join(cfg.ViewCgroups, "view-orphan"), cfg.UIDs.First+1, "/bin/sleep", "60")
	// A crash between creating a view's cgroup and cloning its launcher leaves it empty.
	if err := os.Mkdir(filepath.Join(cfg.ViewCgroups, "view-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.ViewCgroups, "cgroup.max.descendants"), []byte("3"), 0); err != nil {
		t.Fatal(err)
	}
	stdin, hold, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// /bin/sh reads its commands from stdin, so it runs until hold closes.
	id := cfg.UIDs.First + 2
	unrelated := &exec.Cmd{Path: "/bin/sh", Args: []string{"oac-sessionview"}, Stdin: stdin, SysProcAttr: &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID, Credential: &syscall.Credential{Uid: id, Gid: id}}}
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	stdin.Close()

	h, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer h.Close()
	for name, cmd := range map[string]*exec.Cmd{"forking": forking, "orphan": orphan} {
		var exit *exec.ExitError
		if err := cmd.Wait(); !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Errorf("the %s process ended with %v, want SIGKILL", name, err)
		}
	}
	if held, err := heldUIDs(taskUIDs, UIDRange{First: cfg.UIDs.First, Count: 2}); err != nil || len(held) != 0 {
		t.Errorf("Session uids held after Open: %v, %v", held, err)
	}
	if left := sessionviewtest.Cgroups(t, cfg.ViewCgroups); len(left) != 0 {
		t.Errorf("view cgroups left after Open: %v", left)
	}
	if left := leftEntries(t, cfg); len(left) != 0 {
		t.Errorf("transient entries left after Open: %v", left)
	}
	if data, err := os.ReadFile(planted.entry(homeEntry, "history")); err != nil || string(data) != "kept" {
		t.Errorf("the home holds %q, %v after Open", data, err)
	}
	hold.Close()
	if err := unrelated.Wait(); err != nil {
		t.Errorf("the unrelated process ended with %v, want exit 0 at the end of its stdin", err)
	}
	if _, err := Open(cfg); !errors.Is(err, ErrStateLocked) {
		t.Errorf("a second Open = %v, want ErrStateLocked", err)
	}
	other := cfg
	other.StateDir = t.TempDir()
	if _, err := Open(other); !errors.Is(err, ErrStateLocked) {
		t.Errorf("an Open of another StateDir with the same ViewCgroups = %v, want ErrStateLocked", err)
	}
}

// plantSession creates the directory of Session id as an earlier agent host
// leaves it: a home that holds history, and the transient entries.
func plantSession(t *testing.T, cfg Config, id sandboxwire.ID) sessionDir {
	t.Helper()
	dir := sessionDir(filepath.Join(sessionsDir(cfg.StateDir), id.String()))
	for _, name := range []string{homeEntry, etcEntry, maskEntry, stagingEntry} {
		if err := os.MkdirAll(dir.entry(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(dir.entry(homeEntry, "history"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRemoveHome(t *testing.T) {
	cfg := newConfig(t, agent.NewRegistry(), testCA())
	h, id := &Host{cfg: cfg}, sandboxwire.NewID()
	dir := plantSession(t, cfg, id)
	if err := h.RemoveHome(id); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("RemoveHome beside the transient entries = %v, want ErrSessionExists", err)
	}
	if err := dir.clear(); err != nil {
		t.Fatal(err)
	}
	// Removal is idempotent: an absent home is removed.
	for range 2 {
		if err := h.RemoveHome(id); err != nil {
			t.Fatalf("RemoveHome = %v", err)
		}
	}
	if _, err := os.Stat(string(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the Session directory remains: %v", err)
	}
}

// startInCgroup starts args as uid in a new cgroup dir, as a view's processes
// run.
func startInCgroup(t *testing.T, dir string, uid uint32, args ...string) *exec.Cmd {
	t.Helper()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cgroup, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cgroup.Close()
	cmd := &exec.Cmd{Path: args[0], Args: args, SysProcAttr: &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(cgroup.Fd()),
		Credential: &syscall.Credential{Uid: uid, Gid: uid}}}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func testCA() *x509.Certificate {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	srv.Close()
	return srv.Certificate()
}
