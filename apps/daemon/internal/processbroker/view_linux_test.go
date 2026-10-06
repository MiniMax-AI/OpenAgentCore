//go:build linux

package processbroker

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	gofs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// harnessEnv makes the test binary the Harness of TestStuckOutputDoesNotHoldTeardown.
const harnessEnv = "OAC_TEST_VIEW_HARNESS"

func TestViewRunsRemoteShell(t *testing.T) {
	w := newHangWorld(t)
	v, _ := startView(t, w, sessionview.Process{Path: "/bin/sh", Args: []string{"sh", "-c", "echo $0"}})
	out, err := io.ReadAll(v.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := v.Wait(); err != nil || exit != (sessionview.Exit{}) || string(out) != "sh\n" {
		t.Fatalf("Wait = %+v, %v; output %q", exit, err, out)
	}
}

// TestStuckOutputDoesNotHoldTeardown gives a shim's stdout on a world file
// whose writes are never answered. Broker.Close returns while the relay's
// write is stuck, and the view's teardown ends it by stopping the world, as
// the world frontend's Stop does.
func TestStuckOutputDoesNotHoldTeardown(t *testing.T) {
	w := newHangWorld(t)
	v, b := startView(t, w, sessionview.Process{Path: "/.oac/harness/harness", Args: []string{"harness"}, Env: []string{harnessEnv + "=1"}})
	select {
	case <-w.hung:
	case <-time.After(20 * time.Second):
		t.Fatal("the relay never wrote the output")
	}
	started := time.Now()
	b.Close()
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("Broker.Close took %v while the relay's write was stuck", elapsed)
	}
	started = time.Now()
	if err := v.Close(); err != nil {
		t.Errorf("View.Close = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Errorf("View.Close took %v", elapsed)
	}
}

// runHarness runs the shim with its stdout on the world file whose writes
// are never answered.
func runHarness() int {
	f, err := os.OpenFile("/hang", os.O_WRONLY, 0)
	if err == nil {
		err = unix.Dup3(int(f.Fd()), 1, 0)
	}
	if err == nil {
		err = syscall.Exec("/bin/sh", []string{"sh", "-c", "echo hi"}, []string{})
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// startView starts a view whose process runs as viewID with the shim at
// /bin/sh, and a broker for its relay. The test binary is at
// /.oac/harness/harness.
func startView(t *testing.T, w *hangWorld, p sessionview.Process) (*sessionview.View, *Broker) {
	if os.Getenv(viewGateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see the comment at the top of broker_linux_test.go", viewGateEnv)
	}
	if err := sessionview.Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	sock := service.socket(t)
	harness := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harness, "harness"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	p.Dir, p.UID, p.GID, p.Stderr = "/", viewID, viewID, os.Stderr
	v, err := sessionview.Start(context.Background(), sessionview.Spec{
		World:         w.serve,
		Private:       []sessionview.PrivateDir{{Name: "harness", HostDir: harness, Exec: true}},
		Shim:          sessionview.Shim{Binary: shimBinary(t), Paths: []string{"/bin/sh"}},
		Process:       p,
		StagingParent: t.TempDir(),
		CgroupParent:  sessionviewtest.CgroupParent(t),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	b, err := Start(Config{
		Relay:       v.Relay(),
		Executables: Executables{Paths: map[string]string{"/bin/sh": "/bin/sh"}},
		Environment: Environment{Sandbox: map[string]string{"PATH": "/usr/bin:/bin"}},
		Scope:       sp.ScopePOSIXSession,
		Dial: func(ctx context.Context) (io.ReadWriteCloser, error) {
			return new(net.Dialer).DialContext(ctx, "unix", sock)
		},
		CancelGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return v, b
}

// shimBinary returns a static oac-process-shim for the view.
func shimBinary(t *testing.T) string {
	if bin := os.Getenv(shimEnv); bin != "" {
		return bin
	}
	bin := filepath.Join(t.TempDir(), "oac-process-shim")
	if err := build(bin, "../../cmd/oac-process-shim"); err != nil {
		t.Fatal(err)
	}
	return bin
}

// hangWorld serves a directory as the world, the way the world frontend
// serves a sandbox, presenting each mountpoint at its view path. Writes to
// /hang get no answer, even when the writer is killed, until Stop: like the
// world frontend's, Stop ends every request still pending and then waits for
// serving to end.
type hangWorld struct {
	dir           string
	served        chan struct{}
	hung, release chan struct{} // hung closes at the first write to /hang
	hungOnce      sync.Once
	releaseOnce   sync.Once
}

func newHangWorld(t *testing.T) *hangWorld {
	dir := t.TempDir()
	for _, d := range []string{".oac/harness", ".oac/run", ".oac/bin", "proc", "dev", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"bin/sh", "hang"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return &hangWorld{dir: dir, hung: make(chan struct{}), release: make(chan struct{})}
}

func (w *hangWorld) serve(_ context.Context, dev *os.File, mount sessionview.WorldMount) (sessionview.WorldServer, sessionview.Presentation, error) {
	var p sessionview.Presentation
	root, err := gofs.NewLoopbackRoot(w.dir)
	if err != nil {
		return nil, p, err
	}
	root.(*gofs.LoopbackNode).RootData.NewNode = func(r *gofs.LoopbackRoot, parent *gofs.Inode, name string, _ *syscall.Stat_t) gofs.InodeEmbedder {
		n := &gofs.LoopbackNode{RootData: r}
		if name == "hang" && parent.IsRoot() {
			return &hangNode{LoopbackNode: n, w: w}
		}
		return n
	}
	fd, err := unix.Dup(int(dev.Fd()))
	if err != nil {
		return nil, p, err
	}
	srv, err := fuse.NewServer(gofs.NewNodeFS(root, &gofs.Options{}), fmt.Sprintf("/dev/fd/%d", fd), &fuse.MountOptions{})
	if err != nil {
		unix.Close(fd)
		return nil, p, err
	}
	w.served = make(chan struct{})
	go func() {
		srv.Serve()
		close(w.served)
	}()
	for _, m := range mount.Mountpoints {
		p.Targets = append(p.Targets, m.Path)
	}
	return w, p, nil
}

func (w *hangWorld) Stop() error {
	w.releaseOnce.Do(func() { close(w.release) })
	select {
	case <-w.served:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("world still serving 10s after its requests ended")
	}
}

type hangNode struct {
	*gofs.LoopbackNode
	w *hangWorld
}

func (n *hangNode) Write(context.Context, gofs.FileHandle, []byte, int64) (uint32, syscall.Errno) {
	n.w.hungOnce.Do(func() { close(n.w.hung) })
	<-n.w.release
	return 0, syscall.EIO
}
