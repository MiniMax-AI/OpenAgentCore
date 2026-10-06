//go:build linux

package worldfs_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/worldfs"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/fileservicetest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/hanwen/go-fuse/v2/posixtest"
	"golang.org/x/sys/unix"
)

// The world tests mount FUSE and need root with CAP_SYS_ADMIN (and CAP_NET_ADMIN for the view test), /dev/fuse and no AppArmor confinement. Run them in a throwaway container:
//
//	CGO_ENABLED=0 go test -c -o /tmp/worldfs.test ./apps/daemon/internal/worldfs
//	docker run --rm --cgroupns=private --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -e OAC_TEST_WORLDFS=1 -v /tmp/worldfs.test:/t.test:ro debian:bookworm-slim /t.test -test.v
const (
	gateEnv    = "OAC_TEST_WORLDFS"
	helperEnv  = "OAC_WORLDFS_HELPER"
	shimMarker = "oac-test-shim"
)

// The test binary is also the launcher, the Harness, the shim and the relay inside the view.
func TestMain(m *testing.M) {
	sessionview.Init()
	if processshim.Relaying() {
		os.Exit(processshim.Relay())
	}
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
	dir      string
	world    *worldfs.World
	srv      *fileservicetest.Server
	present  sessionview.Presentation
	readDirs atomic.Int64 // ReadDir requests the world sent
	stopped  bool         // the test unmounted and stopped the world itself
}

// counted counts the ReadDir requests written to a stream, one frame per write.
type counted struct {
	io.ReadWriteCloser
	readDirs *atomic.Int64
}

func (c counted) Write(b []byte) (int, error) {
	if len(b) >= 6 && binary.BigEndian.Uint16(b[4:6]) == uint16(sandboxfs.OpReadDir) {
		c.readDirs.Add(1)
	}
	return c.ReadWriteCloser.Write(b)
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
	m := &mounted{dir: mnt, srv: srv}
	m.world = worldfs.New(fileservicetest.Export, func(ctx context.Context) (io.ReadWriteCloser, error) {
		rw, err := srv.Dial(ctx)
		if err != nil {
			return nil, err
		}
		return counted{rw, &m.readDirs}, nil
	})
	ws, p, err := m.world.Serve(context.Background(), dev, sessionview.WorldMount{UID: id, GID: id, Mountpoints: mps})
	m.present = p
	if err == nil {
		// The kernel asks the server about a file's first poll, and the Go runtime polls each file this process opens without releasing its P, which the world needs to answer.
		// Poll go-fuse's own file once with a call that releases the P, as fuse.Server.WaitMount does for other mountpoints, and the kernel stops asking.
		fd, err := unix.Open(filepath.Join(mnt, ".go-fuse-epoll-hack"), unix.O_RDONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 0)
		unix.Close(fd)
	}
	t.Cleanup(func() {
		if !m.stopped {
			if err := unix.Unmount(mnt, unix.MNT_DETACH); err != nil {
				t.Errorf("unmount: %v", err)
			}
			if ws != nil {
				if err := ws.Stop(); err != nil {
					t.Errorf("Stop: %v", err)
				}
			}
		}
		dev.Close()
	})
	return m, err
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

// The operations TestPOSIX skips fail as the package documentation maps them.
func TestUnsupportedErrnos(t *testing.T) {
	requireFUSE(t)
	m, err := serve(t, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	p := filepath.Join(m.dir, "f")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fd := int(f.Fd())
	_, getErr := unix.Getxattr(p, "user.x", make([]byte, 64))
	_, listErr := unix.Listxattr(p, make([]byte, 64))
	for name, c := range map[string]struct{ err, want error }{
		"fallocate":           {unix.Fallocate(fd, 0, 0, 4096), unix.EOPNOTSUPP},
		"fallocate keep size": {unix.Fallocate(fd, unix.FALLOC_FL_KEEP_SIZE, 0, 4096), unix.EOPNOTSUPP},
		"getxattr":            {getErr, unix.EOPNOTSUPP},
		"listxattr":           {listErr, unix.EOPNOTSUPP},
		"setxattr":            {unix.Setxattr(p, "user.x", []byte("v"), 0), unix.EOPNOTSUPP},
		"removexattr":         {unix.Removexattr(p, "user.x"), unix.EOPNOTSUPP},
		"fcntl lock":          {unix.FcntlFlock(uintptr(fd), unix.F_SETLK, &unix.Flock_t{Type: unix.F_WRLCK}), unix.ENOLCK},
	} {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s = %v, want %v", name, c.err, c.want)
		}
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
	// A private mapping reads the file through READ when it faults; a shared one is refused.
	// madvise faults the mapping in within a system call, which releases the P the world needs to answer; a fault on a load would hold it.
	mf, err := os.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := unix.Mmap(int(mf.Fd()), 0, len("before"), unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		t.Fatalf("private mapping: %v", err)
	}
	if err := unix.Madvise(b, unix.MADV_POPULATE_READ); err != nil {
		t.Errorf("fault the private mapping in: %v", err)
	} else if string(b) != "before" {
		t.Errorf("private mapping = %q", b)
	}
	unix.Munmap(b)
	if _, err := unix.Mmap(int(mf.Fd()), 0, len("before"), unix.PROT_READ, unix.MAP_SHARED); !errors.Is(err, syscall.ENODEV) {
		t.Errorf("shared mapping: %v, want ENODEV", err)
	}
	mf.Close()

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

// Append is a property of each write: fcntl(F_SETFL) clears and sets O_APPEND on an open descriptor, as on a native file.
func TestAppendFollowsFcntl(t *testing.T) {
	requireFUSE(t)
	backing := t.TempDir()
	writeFile(t, filepath.Join(backing, "f"), "abc")
	m, err := serve(t, backing, 0)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	fd, err := unix.Open(filepath.Join(m.dir, "f"), unix.O_RDWR|unix.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	want := func(content string) {
		t.Helper()
		if b, err := os.ReadFile(filepath.Join(backing, "f")); err != nil || string(b) != content {
			t.Fatalf("file = %q, %v; want %q", b, err, content)
		}
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Pwrite(fd, []byte("X"), 0); err != nil {
		t.Fatal(err)
	}
	want("Xbc")
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, unix.O_APPEND); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Pwrite(fd, []byte("Y"), 0); err != nil {
		t.Fatal(err)
	}
	want("XbcY")
}

// Stop returns within its bound when the service has become unreachable, although Detach must redial.
func TestStopUnreachable(t *testing.T) {
	requireFUSE(t)
	m, err := serve(t, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	m.stopped = true
	m.srv.Stall()
	if err := unix.Unmount(m.dir, unix.MNT_DETACH); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- m.world.Stop() }()
	select {
	case err := <-stopped:
		if !errors.Is(err, worldfs.ErrConnect) {
			t.Errorf("Stop = %v, want ErrConnect", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Stop did not return within its 15-second bound")
	}
}

// unanswered never answers Attach; it closes attaching once Attach arrives.
type unanswered struct {
	sandboxfs.Service
	attaching chan struct{}
}

func (u unanswered) Attach(ctx context.Context, _ sandboxfs.Attachment, _ *sandboxfs.AttachRequest) (*sandboxfs.AttachResponse, error) {
	close(u.attaching)
	<-ctx.Done()
	return nil, ctx.Err()
}

// A Start that ends while Attach is unanswered reports the cancellation, and the world's Detach on a new stream leaves nothing for the attachment's owner to end.
func TestStartEndsDuringAttach(t *testing.T) {
	requireFUSE(t)
	srv, err := fileservicetest.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	attaching := make(chan struct{})
	srv.Intercept(func(s sandboxfs.Service) sandboxfs.Service { return unanswered{s, attaching} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-attaching
		cancel()
	}()
	_, err = sessionview.Start(ctx, sessionview.Spec{
		World:         worldfs.New(fileservicetest.Export, srv.Dial).Serve,
		StagingParent: t.TempDir(),
		CgroupParent:  sessionviewtest.CgroupParent(t),
		Process:       sessionview.Process{Path: "/bin/true", Args: []string{"true"}, Dir: "/", UID: 1000, GID: 1000, Stderr: os.Stderr},
	})
	if errors.Is(err, worldfs.ErrAttachmentDirty) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Start = %v, want the cancellation without ErrAttachmentDirty", err)
	}
}

// An interrupted flock fails with EINTR and leaves no lock on the view's handle. flock(1) waits with a timer whose signal handler does not restart the call, and it locks the descriptor the test keeps open.
func TestLockInterrupted(t *testing.T) {
	requireFUSE(t)
	flock, err := exec.LookPath("flock")
	if err != nil {
		t.Skip("needs flock(1) from util-linux")
	}
	backing := t.TempDir()
	writeFile(t, filepath.Join(backing, "f"), "")
	m, err := serve(t, backing, 0)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	holder, err := os.Open(filepath.Join(backing, "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := unix.Flock(int(holder.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(m.dir, "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	cmd := exec.Command(flock, "--exclusive", "--timeout", "0.5", "3")
	cmd.ExtraFiles = []*os.File{f}
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("flock(1) = %v, want exit status 1: the timer interrupted the wait", err)
	}
	if err := unix.Flock(int(holder.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	other, err := os.Open(filepath.Join(backing, "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Errorf("the view's handle kept a lock after the interrupted flock: %v", err)
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
