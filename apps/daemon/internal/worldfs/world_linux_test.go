//go:build linux

package worldfs_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/worldfs"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/fileservicetest"
	"github.com/hanwen/go-fuse/v2/posixtest"
	"golang.org/x/sys/unix"
)

// The world tests mount FUSE and need root with CAP_SYS_ADMIN (and CAP_NET_ADMIN for the view test), /dev/fuse and no AppArmor confinement. Run them in a throwaway container:
//
//	CGO_ENABLED=0 go test -c -o /tmp/worldfs.test ./apps/daemon/internal/worldfs
//	docker run --rm --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -e OAC_TEST_WORLDFS=1 -v /tmp/worldfs.test:/t.test:ro debian:bookworm-slim /t.test -test.v
const (
	gateEnv    = "OAC_TEST_WORLDFS"
	helperEnv  = "OAC_WORLDFS_HELPER"
	shimMarker = "oac-test-shim"
)

// The test binary is also the launcher, the Harness and the shim inside the view.
func TestMain(m *testing.M) {
	sessionview.Init()
	if filepath.Base(os.Args[0]) == "sh" {
		fmt.Println(shimMarker)
		os.Exit(0)
	}
	if os.Getenv(helperEnv) != "" {
		os.Exit(runHelper())
	}
	os.Exit(m.Run())
}

// runHelper edits the workspace the way editors do, then runs the shim through the sandbox's /bin symlink.
func runHelper() int {
	if err := os.WriteFile("/data/f.tmp", []byte("edited in the view"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.Rename("/data/f.tmp", "/data/f.txt"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out, err := exec.Command("/bin/sh").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.Stdout.Write(out)
	return 0
}

func requireFUSE(t *testing.T) {
	t.Helper()
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see the comment at the top of this file", gateEnv)
	}
}

type mounted struct {
	dir     string
	world   *worldfs.World
	srv     *fileservicetest.Server
	present sessionview.Presentation
}

// serve mounts a FUSE connection the way the sessionview launcher does and serves the world over backing on it, as view identity id with the given mountpoints.
func serve(t *testing.T, backing string, id uint32, mps ...sessionview.Mountpoint) (*mounted, error) {
	t.Helper()
	srv, err := fileservicetest.New(backing)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	mnt := t.TempDir()
	dev, err := os.OpenFile("/dev/fuse", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	opts := fmt.Sprintf("fd=%d,rootmode=40000,user_id=0,group_id=0,allow_other", dev.Fd())
	if err := unix.Mount("oac-world", mnt, "fuse", unix.MS_NOSUID|unix.MS_NODEV, opts); err != nil {
		dev.Close()
		t.Fatalf("mount: %v", err)
	}
	w := worldfs.New(fileservicetest.Export, srv.Dial)
	ws, p, err := w.Serve(dev, sessionview.WorldMount{UID: id, GID: id, Mountpoints: mps})
	t.Cleanup(func() {
		if err := unix.Unmount(mnt, unix.MNT_DETACH); err != nil {
			t.Errorf("unmount: %v", err)
		}
		if ws != nil {
			if err := ws.Stop(); err != nil {
				t.Errorf("Stop: %v", err)
			}
		}
		dev.Close()
	})
	return &mounted{dir: mnt, world: w, srv: srv, present: p}, err
}

// posixSkips are the posixtest cases outside phase 1, each with the reason.
var posixSkips = map[string]string{
	"Fallocate":           "phase 1 excludes allocation: FALLOCATE is ENOSYS and the kernel answers EOPNOTSUPP",
	"FallocateKeepSize":   "phase 1 excludes allocation: FALLOCATE is ENOSYS and the kernel answers EOPNOTSUPP",
	"FcntlFlockSetLk":     "the file service declares no POSIX locks, so fcntl locks fail with ENOLCK instead of locking only within the view",
	"FcntlFlockLocksFile": "the file service declares no POSIX locks, so fcntl locks fail with ENOLCK instead of locking only within the view",
}

func TestPOSIX(t *testing.T) {
	requireFUSE(t)
	for name, fn := range posixtest.All {
		t.Run(name, func(t *testing.T) {
			if reason, ok := posixSkips[name]; ok {
				t.Skip(reason)
			}
			m, err := serve(t, t.TempDir(), 0)
			if err != nil {
				t.Fatalf("Serve: %v", err)
			}
			fn(t, m.dir)
		})
	}
}

func TestOwnerMapping(t *testing.T) {
	requireFUSE(t)
	const view, other = 1000, 4242
	backing := t.TempDir()
	writeFile(t, filepath.Join(backing, "mine"), "")
	writeFile(t, filepath.Join(backing, "theirs"), "")
	if err := os.Chown(filepath.Join(backing, "theirs"), other, other); err != nil {
		t.Fatal(err)
	}
	m, err := serve(t, backing, view)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	wantOwner(t, filepath.Join(m.dir, "mine"), view)
	wantOwner(t, filepath.Join(m.dir, "theirs"), other)

	if err := os.Chown(filepath.Join(m.dir, "theirs"), view, view); err != nil {
		t.Fatalf("chown to the view identity: %v", err)
	}
	wantOwner(t, filepath.Join(backing, "theirs"), 0)
	wantOwner(t, filepath.Join(m.dir, "theirs"), view)
	if err := os.Chown(filepath.Join(m.dir, "mine"), other, other); err != nil {
		t.Fatalf("chown to another identity: %v", err)
	}
	wantOwner(t, filepath.Join(backing, "mine"), other)

	writeFile(t, filepath.Join(m.dir, "new"), "")
	wantOwner(t, filepath.Join(m.dir, "new"), view)
	wantOwner(t, filepath.Join(backing, "new"), 0)
}

func TestInstanceChanged(t *testing.T) {
	requireFUSE(t)
	backing := t.TempDir()
	writeFile(t, filepath.Join(backing, "f"), "before")
	m, err := serve(t, backing, 0)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	f := filepath.Join(m.dir, "f")
	if _, err := os.ReadFile(f); err != nil {
		t.Fatal(err)
	}

	// A lost stream to the same incarnation redials. A request that raced the break may fail; the next one must succeed.
	m.srv.Break()
	if _, err := os.ReadFile(f); err != nil {
		if _, err := os.ReadFile(f); err != nil {
			t.Fatalf("read after a lost stream: %v", err)
		}
	}
	select {
	case <-m.world.Lost():
		t.Fatalf("world lost after a lost stream: %v", m.world.Err())
	default:
	}

	if err := m.srv.Restart(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(f); !errors.Is(err, syscall.EIO) {
		t.Fatalf("read after restart = %v, want EIO", err)
	}
	waitLost(t, m.world, worldfs.ErrInstanceChanged)
	if _, err := os.Stat(filepath.Join(m.dir, "g")); !errors.Is(err, syscall.EIO) {
		t.Errorf("lookup after restart = %v, want EIO", err)
	}
}

func waitLost(t *testing.T, w *worldfs.World, kind error) {
	t.Helper()
	select {
	case <-w.Lost():
	case <-time.After(10 * time.Second):
		t.Fatal("Lost never closed")
	}
	if !errors.Is(w.Err(), kind) {
		t.Fatalf("Err = %v, want %v", w.Err(), kind)
	}
}

func wantOwner(t *testing.T, p string, id uint32) {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		t.Fatal(err)
	}
	if st.Uid != id || st.Gid != id {
		t.Errorf("%s owned by %d:%d, want %d:%d", p, st.Uid, st.Gid, id, id)
	}
}

func writeFile(t *testing.T, p, data string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
