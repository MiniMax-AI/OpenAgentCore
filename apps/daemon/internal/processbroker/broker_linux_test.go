//go:build linux

package processbroker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	gofs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// The sandbox side is the Linux process service in its own process,
// processserve, because the service reaps every child of its process and
// this binary waits on the shims it starts. Locally the tests build
// processserve and the shim with the go command. The view test needs root in
// a privileged container, with static binaries:
//
//	CGO_ENABLED=0 go test -c -o /tmp/processbroker.test ./apps/daemon/internal/processbroker
//	CGO_ENABLED=0 go build -o /tmp/processserve ./apps/sandboxio/testdata/processserve
//	CGO_ENABLED=0 go build -o /tmp/oac-process-shim ./apps/daemon/cmd/oac-process-shim
//	docker run --rm --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -e OAC_TEST_SESSIONVIEW=1 -e OAC_TEST_PROCESS_SERVICE=/svc -e OAC_TEST_PROCESS_SHIM=/shim \
//	  -v /tmp/processbroker.test:/t.test:ro -v /tmp/processserve:/svc:ro -v /tmp/oac-process-shim:/shim:ro \
//	  debian:bookworm-slim /t.test -test.v
const (
	shimSocketEnv = "OAC_TEST_SHIM_SOCKET"
	serviceEnv    = "OAC_TEST_PROCESS_SERVICE"
	shimEnv       = "OAC_TEST_PROCESS_SHIM"
	viewGateEnv   = "OAC_TEST_SESSIONVIEW"
	viewID        = 1000
)

// The test binary is also the shim, through links named after the commands.
func TestMain(m *testing.M) {
	sessionview.Init()
	if sock := os.Getenv(shimSocketEnv); sock != "" {
		os.Exit(processshim.Run(sock))
	}
	code := m.Run()
	service.stop()
	os.Exit(code)
}

func TestStreamsAndExitCode(t *testing.T) {
	f := newFixture(t, 0)
	cmd := f.command("bash", "-c", "echo out; echo err >&2; exit 3")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exitCode(err) != 3 || stdout.String() != "out\n" || stderr.String() != "err\n" {
		t.Fatalf("Run = %v; stdout %q, stderr %q", err, stdout.String(), stderr.String())
	}
}

func TestShimExitsBeforeBackgroundJobOutput(t *testing.T) {
	f := newFixture(t, 0)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd := f.command("sh", "-c", "sleep 1 >/dev/null & echo hi")
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	read := make(chan []byte)
	go func() {
		data, _ := io.ReadAll(r)
		read <- data
	}()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait = %v", err)
	}
	select {
	case data := <-read:
		t.Fatalf("pipe closed before the shim exited; read %q", data)
	default:
	}
	select {
	case data := <-read:
		if string(data) != "hi\n" {
			t.Fatalf("read %q", data)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pipe still open 10s after the job should have ended")
	}
}

func TestRemoteSignalDeath(t *testing.T) {
	f := newFixture(t, 0)
	err := f.command("sh", "-c", "kill -TERM $$").Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("Run = %v", err)
	}
	if ws := ee.Sys().(syscall.WaitStatus); !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("status = %v", ws)
	}
}

func TestInterruptReachesRemoteGroup(t *testing.T) {
	f := newFixture(t, 0)
	cmd := f.command("bash", "-c", "trap 'echo interrupted; exit 7' INT; echo ready; while :; do sleep 0.1; done")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := startLine(t, cmd, "ready")
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(out)
	if err := cmd.Wait(); exitCode(err) != 7 || string(rest) != "interrupted\n" {
		t.Fatalf("Wait = %v; output %q", err, rest)
	}
}

func TestKilledShimCancelsRemoteScope(t *testing.T) {
	f := newFixture(t, 0)
	cmd := f.command("bash", "-c", "sleep 1000 & echo $!; wait")
	out := bufio.NewReader(start(t, cmd))
	line, err := out.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(line)
	if _, err := os.Stat("/proc/" + pid); err != nil {
		t.Fatalf("remote job %s: %v", pid, err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat("/proc/" + pid); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote job %s still running 10s after the shim died", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTerminalSizeAndMode(t *testing.T) {
	f := newFixture(t, 0)
	ptm, pts, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer ptm.Close()
	defer pts.Close()
	if err := pty.Setsize(ptm, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	before, err := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	cmd := f.command("bash", "-c", `stty size; while [ "$(stty size)" = "30 100" ]; do sleep 0.1; done; stty size`)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pts, pts, pts
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var (
		mu  sync.Mutex
		out bytes.Buffer
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptm.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	output := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(output(), "30 100\r\n"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no initial size; output %q", output())
		}
	}
	// The kernel sends SIGWINCH to the shim, the terminal's foreground group.
	if err := pty.Setsize(ptm, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait = %v; output %q", err, output())
	}
	after, err := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before {
		t.Fatalf("terminal mode not restored:\nbefore %+v\nafter  %+v", *before, *after)
	}
	pts.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("terminal still open 10s after the exit; output %q", output())
	}
	if got := output(); got != "30 100\r\n40 120\r\n" {
		t.Fatalf("output %q", got)
	}
}

func TestEnvironmentIsDeclaredOnly(t *testing.T) {
	f := newFixture(t, 0)
	cmd := f.command("env")
	cmd.Env = append(cmd.Env, "KEEP=kept", "LEAK=/x/.oac/run", "OTHER=dropped", "HOME=/local/home")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output = %v", err)
	}
	want := "HOME=/home/sandbox\nKEEP=kept\nLANG=C.UTF-8\nPATH=/usr/bin:/bin\nTOOL=1\n"
	if string(out) != want || bytes.Contains(out, []byte("/.oac")) {
		t.Fatalf("remote environment %q, want %q", out, want)
	}
}

func TestLinkLossKeepsOutputOrdered(t *testing.T) {
	f := newFixture(t, 64<<10)
	out, err := f.command("seq", "1", "2000000").Output()
	if err != nil {
		t.Fatalf("Output = %v", err)
	}
	var want []byte
	for i := 1; i <= 2000000; i++ {
		want = strconv.AppendInt(want, int64(i), 10)
		want = append(want, '\n')
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("output differs: %d bytes, want %d", len(out), len(want))
	}
	if n := f.dials.Load(); n < 2 {
		t.Fatalf("%d dials; the link was never cut", n)
	}
}

func TestViewRunsRemoteShell(t *testing.T) {
	if os.Getenv(viewGateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see the comment at the top of this file", viewGateEnv)
	}
	if err := sessionview.Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	sock := service.socket(t)
	base := t.TempDir()
	world, run := filepath.Join(base, "world"), filepath.Join(base, "run")
	for _, d := range []string{".oac/run", ".oac/bin", "proc", "dev", "bin"} {
		if err := os.MkdirAll(filepath.Join(world, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(world, "bin", "sh"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(run, viewID, viewID); err != nil {
		t.Fatal(err)
	}
	b, err := Start(Config{
		RunDir:      run,
		UID:         viewID,
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
	defer b.Close()
	w := &loopbackWorld{dir: world}
	v, err := sessionview.Start(context.Background(), sessionview.Spec{
		World:   w.serve,
		Private: []sessionview.PrivateDir{{Name: "run", HostDir: run, Writable: true}},
		Shim:    sessionview.Shim{Binary: shimBinary(t), Paths: []string{"/bin/sh"}},
		Process: sessionview.Process{Path: "/bin/sh", Args: []string{"sh", "-c", "echo $0"}, Dir: "/", UID: viewID, GID: viewID, Stderr: os.Stderr},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	out, err := io.ReadAll(v.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := v.Wait(); err != nil || exit != (sessionview.Exit{}) || string(out) != "sh\n" {
		t.Fatalf("Wait = %+v, %v; output %q", exit, err, out)
	}
}

type fixture struct {
	dir, bin string
	service  string
	cut      int
	dials    atomic.Int32
}

// newFixture starts a broker whose shims are this binary under the names
// bash, sh, env and seq. A positive cut drops the first stream after that
// many bytes from the service.
func newFixture(t *testing.T, cut int) *fixture {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: t.TempDir(), service: service.socket(t), cut: cut}
	f.bin = filepath.Join(f.dir, "bin")
	if err := os.Mkdir(f.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, name := range []string{"bash", "sh", "env", "seq"} {
		local := filepath.Join(f.bin, name)
		if err := os.Symlink(self, local); err != nil {
			t.Fatal(err)
		}
		paths[local] = name
	}
	b, err := Start(Config{
		RunDir:      f.dir,
		UID:         os.Getuid(),
		Executables: Executables{Paths: paths},
		Environment: Environment{
			Pass:    []string{"KEEP", "LEAK"},
			Sandbox: map[string]string{"PATH": "/usr/bin:/bin", "HOME": "/home/sandbox", "LANG": "C.UTF-8"},
			Tool:    map[string]string{"TOOL": "1"},
		},
		Scope:       sp.ScopePOSIXSession,
		Dial:        f.dial,
		CancelGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return f
}

func (f *fixture) command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(filepath.Join(f.bin, name), args...)
	// A race-enabled shim would otherwise sleep a second before exiting.
	cmd.Env = []string{shimSocketEnv + "=" + filepath.Join(f.dir, processshim.SocketName), "GORACE=atexit_sleep_ms=0"}
	cmd.Dir = f.dir
	return cmd
}

func (f *fixture) dial(ctx context.Context) (io.ReadWriteCloser, error) {
	c, err := new(net.Dialer).DialContext(ctx, "unix", f.service)
	if err != nil {
		return nil, err
	}
	if f.dials.Add(1) == 1 && f.cut > 0 {
		return &cutConn{Conn: c, left: f.cut}, nil
	}
	return c, nil
}

// cutConn loses the link after left bytes, mid-frame if it falls there.
type cutConn struct {
	net.Conn
	left int
}

func (c *cutConn) Read(p []byte) (int, error) {
	if c.left <= 0 {
		c.Conn.Close()
		return 0, net.ErrClosed
	}
	n, err := c.Conn.Read(p[:min(len(p), c.left)])
	c.left -= n
	return n, err
}

func start(t *testing.T, cmd *exec.Cmd) io.Reader {
	t.Helper()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return out
}

// startLine starts cmd and reads its first line, which must be want.
func startLine(t *testing.T, cmd *exec.Cmd, want string) io.Reader {
	t.Helper()
	out := bufio.NewReader(start(t, cmd))
	if line, err := out.ReadString('\n'); err != nil || line != want+"\n" {
		t.Fatalf("first line %q, %v", line, err)
	}
	return out
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err == nil {
		return 0
	}
	return -1
}

// processService is the process service the tests share.
type processService struct {
	once sync.Once
	dir  string
	sock string
	cmd  *exec.Cmd
	err  error
}

var service processService

func (s *processService) socket(t *testing.T) string {
	t.Helper()
	s.once.Do(func() { s.err = s.start() })
	if s.err != nil {
		t.Fatalf("process service: %v", s.err)
	}
	return s.sock
}

func (s *processService) start() error {
	dir, err := os.MkdirTemp("", "processbroker")
	if err != nil {
		return err
	}
	s.dir = dir
	bin := os.Getenv(serviceEnv)
	if bin == "" {
		bin = filepath.Join(dir, "processserve")
		if err := build(bin, "../../../sandboxio/testdata/processserve"); err != nil {
			return err
		}
	}
	s.sock = filepath.Join(dir, "service.sock")
	s.cmd = exec.Command(bin, s.sock)
	s.cmd.Stderr = os.Stderr
	out, err := s.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := s.cmd.Start(); err != nil {
		return err
	}
	if line, err := bufio.NewReader(out).ReadString('\n'); line != "ready\n" {
		return fmt.Errorf("service said %q: %v", line, err)
	}
	return nil
}

func (s *processService) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
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

func build(out, pkg string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %v\n%s", pkg, err, msg)
	}
	return nil
}

// loopbackWorld serves a directory as the world, the way the world frontend
// serves a sandbox.
type loopbackWorld struct {
	dir    string
	served chan struct{}
}

func (w *loopbackWorld) serve(dev *os.File, _ sessionview.WorldMount) (sessionview.WorldServer, error) {
	fd, err := unix.Dup(int(dev.Fd()))
	if err != nil {
		return nil, err
	}
	root, err := gofs.NewLoopbackRoot(w.dir)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	srv, err := fuse.NewServer(gofs.NewNodeFS(root, &gofs.Options{}), fmt.Sprintf("/dev/fd/%d", fd), &fuse.MountOptions{})
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	w.served = make(chan struct{})
	go func() {
		srv.Serve()
		close(w.served)
	}()
	return w, nil
}

func (w *loopbackWorld) Stop() error {
	select {
	case <-w.served:
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("world still serving 10s after the view ended")
	}
}
