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
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// The sandbox side is the Linux process service in its own process,
// processserve, because the service reaps every child of its process and
// this binary waits on the shims it starts. Locally the tests build
// processserve with the go command. As root, the relay runs as viewID, as
// in a view. The view tests need root in a privileged container, with
// static binaries:
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
	relayEnv      = "OAC_TEST_RELAY"
	serviceEnv    = "OAC_TEST_PROCESS_SERVICE"
	shimEnv       = "OAC_TEST_PROCESS_SHIM"
	viewGateEnv   = "OAC_TEST_SESSIONVIEW"
	viewID        = 1000
)

// The test binary is also the relay, the shim through links named after the
// commands, and a Harness in the view.
func TestMain(m *testing.M) {
	sessionview.Init()
	if os.Getenv(relayEnv) == "1" {
		os.Exit(processshim.Relay())
	}
	if sock := os.Getenv(shimSocketEnv); sock != "" {
		os.Exit(processshim.Run(sock))
	}
	if os.Getenv(harnessEnv) == "1" {
		os.Exit(runHarness())
	}
	code := m.Run()
	service.stop()
	os.Exit(code)
}

func TestStreamsAndExitCode(t *testing.T) {
	f := newFixture(t, nil)
	cmd := f.command("bash", "-c", "echo out; echo err >&2; exit 3")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exitCode(err) != 3 || stdout.String() != "out\n" || stderr.String() != "err\n" {
		t.Fatalf("Run = %v; stdout %q, stderr %q", err, stdout.String(), stderr.String())
	}
}

// The shim exits with the leader, and output a background job writes later
// still reaches the shim's stdout.
func TestBackgroundOutputAfterShimExits(t *testing.T) {
	f := newFixture(t, nil)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd := f.command("sh", "-c", "(sleep 1; echo late) & echo hi")
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
		if string(data) != "hi\nlate\n" {
			t.Fatalf("read %q", data)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pipe still open 10s after the job should have ended")
	}
}

func TestRemoteSignalDeath(t *testing.T) {
	f := newFixture(t, nil)
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
	f := newFixture(t, nil)
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
	f := newFixture(t, nil)
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
	f := newFixture(t, nil)
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
	f := newFixture(t, nil)
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
	f := newFixture(t, first(func(c net.Conn) io.ReadWriteCloser { return &cutConn{Conn: c, left: 64 << 10} }))
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

// A shim request that never completes holds neither another invocation nor
// Close, and the descriptors it carried close with the relay.
func TestIncompleteRequestHoldsNothing(t *testing.T) {
	f := newFixture(t, nil)
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(f.dir, processshim.SocketName), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var p [2]int
	if err := unix.Pipe2(p[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(p[0])
	var req bytes.Buffer
	if err := sandboxwire.WriteFrame(&req, processshim.Frame(processshim.Request{Version: processshim.Version})); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.WriteMsgUnix(req.Bytes()[:req.Len()/2], unix.UnixRights(p[1], p[1], p[1]), nil)
	unix.Close(p[1])
	if err != nil {
		t.Fatal(err)
	}
	if out, err := f.command("sh", "-c", "echo ok").Output(); err != nil || string(out) != "ok\n" {
		t.Fatalf("Output = %q, %v", out, err)
	}
	closed := make(chan struct{})
	go func() {
		f.broker.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waits for the incomplete request")
	}
	pfd := []unix.PollFd{{Fd: int32(p[0]), Events: unix.POLLIN}}
	if n, err := unix.Poll(pfd, 10000); n != 1 || err != nil || pfd[0].Revents&unix.POLLHUP == 0 {
		t.Fatalf("the passed descriptors are still open: poll = %d, %v, revents %#x", n, err, pfd[0].Revents)
	}
}

// A gone output reader must not hold the exit back while the stream that
// would close the remote output is lost behind the settled operation.
func TestGoneReaderDoesNotHoldExit(t *testing.T) {
	f := newFixture(t, first(func(c net.Conn) io.ReadWriteCloser { return &holdEvents{Conn: c} }))
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	cmd := f.command("sh", "-c", "echo hi")
	cmd.Stdout = w
	err = cmd.Start()
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			t.Fatalf("Wait = %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the shim did not exit")
	}
}

// A receiver with SO_PASSCRED sees the relay's own credentials on output:
// its pid and the Session's uid and gid.
func TestUnixSocketOutputNamesTheRelay(t *testing.T) {
	f := newFixture(t, nil)
	sv, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(sv[0])
	if err := unix.SetsockoptInt(sv[0], unix.SOL_SOCKET, unix.SO_PASSCRED, 1); err != nil {
		t.Fatal(err)
	}
	out := os.NewFile(uintptr(sv[1]), "output")
	cmd := f.command("sh", "-c", "echo hi")
	cmd.Stdout = out
	err = cmd.Start()
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	buf, oob := make([]byte, 64), make([]byte, unix.CmsgSpace(unix.SizeofUcred))
	n, oobn, _, _, err := unix.Recvmsg(sv[0], buf, oob, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait = %v", err)
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) != 1 {
		t.Fatalf("control messages %v, %v", msgs, err)
	}
	cred, err := unix.ParseUnixCredentials(&msgs[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "hi\n" || int(cred.Pid) != f.relay.Process.Pid || int(cred.Uid) != f.uid || int(cred.Gid) != f.gid {
		t.Fatalf("read %q from %+v; the relay is pid %d, uid %d, gid %d", buf[:n], *cred, f.relay.Process.Pid, f.uid, f.gid)
	}
}

// A write the kernel refuses the Session fails as a write failure, never
// with the daemon's authority: lowering oom_score_adj needs
// CAP_SYS_RESOURCE, which the relay lacks.
func TestOutputWritesHaveTheSessionsAuthority(t *testing.T) {
	f := newFixture(t, nil)
	before, err := os.ReadFile("/proc/self/oom_score_adj")
	if err != nil {
		t.Fatal(err)
	}
	adj, err := os.OpenFile("/proc/self/oom_score_adj", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer adj.Close()
	cmd := f.command("bash", "-c", `trap "" PIPE; echo -1000; while echo x; do sleep 0.05; done 2>/dev/null; exit 3`)
	cmd.Stdout = adj
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if exitCode(err) != 3 {
			t.Fatalf("Wait = %v, want exit 3 after the remote output closed", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the failed write never closed the remote output")
	}
	if after, err := os.ReadFile("/proc/self/oom_score_adj"); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("oom_score_adj %q, %v; was %q", after, err, before)
	}
}

// Output that nobody reads holds back only its own invocation: another
// invocation starts, reads stdin, gets a signal and exits, and the blocked
// one still gets its signal and its cancel.
func TestBlockedOutputHoldsOnlyItsInvocation(t *testing.T) {
	f := newFixture(t, nil)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a := f.command("bash", "-c", `echo $$ > a.pid; trap 'touch a.int' INT; while :; do yes; done`)
	a.Stdout = w
	err = a.Start()
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Process.Kill()
	size, err := unix.FcntlInt(r.Fd(), unix.F_GETPIPE_SZ, 0)
	if err != nil {
		t.Fatal(err)
	}
	await(t, "the first invocation's pipe to fill", func() bool {
		n, err := unix.IoctlGetInt(int(r.Fd()), unix.TIOCINQ) // FIONREAD
		return err == nil && n == size
	})

	b := f.command("bash", "-c", `trap 'exit 6' TERM; read line; echo "got $line"; while :; do sleep 0.05; done`)
	b.Stdin = strings.NewReader("hello\n")
	startLine(t, b, "got hello")
	if err := b.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := b.Wait(); exitCode(err) != 6 {
		t.Fatalf("second Wait = %v, want exit 6", err)
	}

	if err := a.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	await(t, "the blocked invocation's trap", func() bool {
		_, err := os.Stat(filepath.Join(f.dir, "a.int"))
		return err == nil
	})
	pid, err := os.ReadFile(filepath.Join(f.dir, "a.pid"))
	if err != nil {
		t.Fatal(err)
	}
	remote := "/proc/" + strings.TrimSpace(string(pid))
	a.Process.Kill()
	a.Wait()
	await(t, "the blocked invocation's cancel", func() bool {
		_, err := os.Stat(remote)
		return errors.Is(err, os.ErrNotExist)
	})
}

// The broker never takes a descriptor from the relay: one sent with a
// message is closed as the broker reads it.
func TestRelayDescriptorsAreDiscarded(t *testing.T) {
	sv, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	end := os.NewFile(uintptr(sv[0]), "broker")
	b, err := Start(Config{
		Relay: end,
		Scope: sp.ScopePOSIXSession,
		Dial:  func(context.Context) (io.ReadWriteCloser, error) { return nil, errors.New("no sandbox") },
	})
	end.Close()
	if err != nil {
		unix.Close(sv[1])
		t.Fatal(err)
	}
	defer b.Close()
	rf := os.NewFile(uintptr(sv[1]), "relay")
	c, err := net.FileConn(rf)
	rf.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		b.Close()
		c.Close()
	}()
	var p [2]int
	if err := unix.Pipe2(p[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(p[0])
	var open bytes.Buffer
	req := processshim.Request{Version: processshim.Version, ExecPath: []byte("/bin/undeclared"), Argv: [][]byte{[]byte("undeclared")}, Cwd: []byte("/")}
	if err := sandboxwire.WriteFrame(&open, processshim.Frame(processshim.Open{ID: 1, Request: req})); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.(*net.UnixConn).WriteMsgUnix(open.Bytes(), unix.UnixRights(p[1]), nil)
	unix.Close(p[1])
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		fr, err := sandboxwire.ReadFrame(c, processshim.MaxFrameBytes)
		if err != nil {
			t.Fatal(err)
		}
		m, err := processshim.DecodeBroker(fr)
		if _, ok := m.(processshim.StopInput); ok {
			continue
		}
		if exit, ok := m.(processshim.Exit); err != nil || !ok || exit.Result.Code != processshim.ExitNotFound {
			t.Fatalf("broker answered %#v, %v", m, err)
		}
		break
	}
	pfd := []unix.PollFd{{Fd: int32(p[0]), Events: unix.POLLIN}}
	if n, err := unix.Poll(pfd, 5000); n != 1 || err != nil || pfd[0].Revents&unix.POLLHUP == 0 {
		t.Fatalf("the broker holds the relay's descriptor: poll = %d, %v, revents %#x", n, err, pfd[0].Revents)
	}
	if err := b.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
}

// A Busy acknowledgement is retried, or output past the replay limit would
// never arrive.
func TestBusyAckIsRetried(t *testing.T) {
	f := newFixture(t, first(func(c net.Conn) io.ReadWriteCloser { return &busyAck{Conn: c} }))
	cmd := f.command("seq", "1", "2000000")
	var out countWriter
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil || out.n != 14888896 {
			t.Fatalf("Wait = %v after %d bytes", err, out.n)
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("output stalled after a Busy acknowledgement")
	}
}

// Concurrent invocations on one terminal leave it as it was: the first saves
// its mode and the last restores it.
func TestSharedTerminalRestoredByLastUser(t *testing.T) {
	f := newFixture(t, nil)
	ptm, pts, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer ptm.Close()
	defer pts.Close()
	go io.Copy(io.Discard, ptm)
	mode := func() *unix.Termios {
		tio, err := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
		if err != nil {
			t.Fatal(err)
		}
		return tio
	}
	raw := func() bool { return mode().Lflag&unix.ICANON == 0 }
	file := func(name string) string { return filepath.Join(f.dir, name) }
	exists := func(name string) func() bool {
		return func() bool { _, err := os.Stat(file(name)); return err == nil }
	}
	before := mode()
	run := func(script string) *exec.Cmd {
		cmd := f.command("bash", "-c", script)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = pts, pts, pts
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	a := run("until [ -e a.done ]; do sleep 0.05; done")
	await(t, "the first invocation to make the terminal raw", raw)
	b := run("touch b.up; until [ -e b.done ]; do sleep 0.05; done")
	await(t, "the second invocation to run", exists("b.up"))
	os.WriteFile(file("a.done"), nil, 0o644)
	if err := a.Wait(); err != nil {
		t.Fatalf("first Wait = %v", err)
	}
	if !raw() {
		t.Fatal("the first invocation to leave restored the terminal under the second")
	}
	os.WriteFile(file("b.done"), nil, 0o644)
	if err := b.Wait(); err != nil {
		t.Fatalf("second Wait = %v", err)
	}
	if after := mode(); *after != *before {
		t.Fatalf("terminal mode not restored:\nbefore %+v\nafter  %+v", *before, *after)
	}
}

// A Busy stdin write is repeated from the first byte the service did not
// accept, so the program reads every byte once.
func TestBusyStdinIsRetried(t *testing.T) {
	var writes atomic.Int32
	f := newFixture(t, first(func(c net.Conn) io.ReadWriteCloser {
		return intercept(c, func(fr sandboxwire.Frame) verdict {
			if fr.Type == sp.OpWriteStdin && writes.Add(1) == 2 {
				return refuseBusy
			}
			return pass
		})
	}))
	var in []byte
	for i := range 30000 {
		in = strconv.AppendInt(in, int64(i), 10)
		in = append(in, '\n')
	}
	cmd := f.command("sh", "-c", "cat")
	cmd.Stdin = bytes.NewReader(in)
	wait := make(chan error, 1)
	var out []byte
	go func() {
		var err error
		out, err = cmd.Output()
		wait <- err
	}()
	select {
	case err := <-wait:
		if err != nil || !bytes.Equal(out, in) || writes.Load() < 2 {
			t.Fatalf("Output = %v after %d stdin writes; read %d bytes of %d, equal %v", err, writes.Load(), len(out), len(in), bytes.Equal(out, in))
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("stdin stalled after a Busy write")
	}
}

// A Cancel whose response is lost with its stream is not sent again: the
// scope gets one TERM, and KILL after the grace.
func TestUncertainCancelIsNotReplayed(t *testing.T) {
	var cancels atomic.Int32
	f := newFixture(t, func(_ int32, c net.Conn) io.ReadWriteCloser {
		return intercept(c, func(fr sandboxwire.Frame) verdict {
			if fr.Type == sp.OpCancel && cancels.Add(1) == 1 {
				return loseResponse
			}
			return pass
		})
	})
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd := f.command("sh", "-c", "trap '' TERM; echo up; sleep 1000")
	cmd.Stdout = w
	err = cmd.Start()
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := bufio.NewReader(r)
	if line, err := out.ReadString('\n'); line != "up\n" {
		t.Fatalf("first line %q, %v", line, err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(out)
		read <- err
	}()
	select {
	case <-read:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled program still runs")
	}
	if n := cancels.Load(); n != 1 {
		t.Fatalf("%d Cancel requests, want 1", n)
	}
}

// Losing the shim while the first stream is still connecting ends the
// invocation: the broker closes the shim's descriptors.
func TestShimLossEndsWaitForStream(t *testing.T) {
	dialed := make(chan struct{})
	f := newFixture(t, func(n int32, c net.Conn) io.ReadWriteCloser {
		if n > 1 {
			return c
		}
		c.Close()
		close(dialed)
		// A service that never answers.
		broker, svc := net.Pipe()
		go io.Copy(io.Discard, svc)
		return broker
	})
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd := f.command("sh", "-c", "echo started")
	cmd.Stdout = w
	err = cmd.Start()
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialed:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the broker never dialed")
	}
	cmd.Process.Kill()
	cmd.Wait()
	read := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(r)
		read <- data
	}()
	select {
	case data := <-read:
		if len(data) != 0 {
			t.Fatalf("read %q", data)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the broker still holds the lost shim's descriptors")
	}
}

// A Start retried after its response was lost finds the operation still
// starting. The program runs, and gets stdin, only once Started arrives: it
// reads all of its input and then end of file. A StartFailed event instead
// is the typed start failure.
func TestRetriedStartWaitsForStarted(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprintf("start fails %v", fails), func(t *testing.T) {
			svc := newFakeService()
			svc.startLate, svc.startDelay, svc.startFails = true, 300*time.Millisecond, fails
			f := newFixture(t, svc.serve(t.Context(), func(fr sandboxwire.Frame) bool { return fr.Type == sp.OpStart }))
			const in = "stdin for a program that was still starting\n"
			out, stderr, err := runFor(t, f.command("sh", "-c", "cat"), in)
			refused := svc.counts().refusedIn
			switch {
			case fails && (exitCode(err) != processshim.ExitNotFound || !strings.Contains(stderr, "no such file")):
				t.Fatalf("Run = %v; stderr %q", err, stderr)
			case !fails && (err != nil || out != in || refused != 0):
				t.Fatalf("Run = %v after %d stdin requests refused while starting; stdout %q, stderr %q", err, refused, out, stderr)
			}
		})
	}
}

// A stdin write lost with its stream after the service took part of it
// resumes from the offset Inspect reports: the program reads every byte
// once, and stdin closes at the end of the input.
func TestUncertainStdinWriteResumes(t *testing.T) {
	svc := newFakeService()
	svc.partialFirst = true
	f := newFixture(t, svc.serve(t.Context(), func(fr sandboxwire.Frame) bool { return fr.Type == sp.OpWriteStdin }))
	const in = "stdin that crosses a lost stream\n"
	out, stderr, err := runFor(t, f.command("sh", "-c", "cat"), in)
	c := svc.counts()
	if err != nil || out != in || string(c.stdin) != in || c.closedAt != uint64(len(in)) || c.writes < 2 {
		t.Fatalf("Run = %v after %d writes; stdout %q, stderr %q; the service took %q and closed stdin at %d", err, c.writes, out, stderr, c.stdin, c.closedAt)
	}
}

// When the accepted stdin offset cannot be learned after a lost write, the
// shim exits with 255 and the reason, and the program is cancelled, rather
// than both waiting for input that never comes.
func TestUnresolvedStdinEndsTheInvocation(t *testing.T) {
	svc := newFakeService()
	svc.partialFirst, svc.inspectFails = true, true
	f := newFixture(t, svc.serve(t.Context(), func(fr sandboxwire.Frame) bool { return fr.Type == sp.OpWriteStdin }))
	_, stderr, err := runFor(t, f.command("sh", "-c", "cat"), "stdin\n")
	if exitCode(err) != processshim.ExitLost || !strings.Contains(stderr, "stdin could not be resumed: inspect failed") {
		t.Fatalf("Run = %v; stderr %q", err, stderr)
	}
	await(t, "the program's cancel", func() bool { return svc.counts().cancels == 1 })
}

// A background process outlives the leader, which exits as it takes a stdin
// write whose response is lost. Inspect shows the leader exited, perhaps
// before its Exited event arrives: stdin closes after the bytes the service
// took, the background process gets end of file, and the operation settles.
func TestBackgroundReaderGetsEOFAfterUncertainWrite(t *testing.T) {
	svc := newFakeService()
	svc.leaderExits = true
	f := newFixture(t, svc.serve(t.Context(), func(fr sandboxwire.Frame) bool { return fr.Type == sp.OpWriteStdin }))
	const in = "stdin for a background reader\n"
	out, stderr, err := runFor(t, f.command("sh", "-c", "cat"), in)
	if err != nil || out != in {
		t.Fatalf("Run = %v; stdout %q, stderr %q", err, out, stderr)
	}
	await(t, "the operation's release", func() bool { return svc.counts().released })
	if c := svc.counts(); c.closedAt != uint64(len(in)) {
		t.Fatalf("stdin closed at %d, not %d", c.closedAt, len(in))
	}
}

// runFor runs cmd with stdin in and returns its stdout, stderr and error. It
// fails the test when cmd still runs after 10s; output the relay still
// holds open 5s after cmd exits is an exec.ErrWaitDelay error.
func runFor(t *testing.T, cmd *exec.Cmd, in string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(in), &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return stdout.String(), stderr.String(), err
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatalf("%s still ran after 10s; stdout %q, stderr %q", cmd.Args, stdout.String(), stderr.String())
		return "", "", nil
	}
}

type fixture struct {
	dir, bin string
	uid, gid int // the relay's
	service  string
	wrap     func(int32, net.Conn) io.ReadWriteCloser
	dials    atomic.Int32
	relay    *exec.Cmd
	broker   *Broker
}

// newFixture starts a relay and its broker, whose shims are this binary
// under the names bash, sh, env and seq. As root the relay runs as viewID. A
// non-nil wrap wraps each stream to the service, which it gets with the
// stream's number, counting from 1.
func newFixture(t *testing.T, wrap func(int32, net.Conn) io.ReadWriteCloser) *fixture {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "processbroker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: dir, uid: os.Getuid(), gid: os.Getgid(), service: service.socket(t), wrap: wrap}
	if f.uid == 0 {
		f.uid, f.gid = viewID, viewID
	}
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
	end := f.startRelay(t, self)
	b, err := Start(Config{
		Relay:       end,
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
	end.Close()
	if err != nil {
		f.relay.Process.Kill()
		f.relay.Wait()
		t.Fatal(err)
	}
	f.broker = b
	t.Cleanup(func() {
		b.Close()
		f.waitRelay(t)
	})
	return f
}

// startRelay starts this binary as the relay, listening at the shim socket
// in f.dir, and returns the broker's end of its connection.
func (f *fixture) startRelay(t *testing.T, self string) *os.File {
	t.Helper()
	sv, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	end, relayEnd := os.NewFile(uintptr(sv[0]), "broker"), os.NewFile(uintptr(sv[1]), "relay")
	defer relayEnd.Close()
	sock := filepath.Join(f.dir, processshim.SocketName)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		end.Close()
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	lf, err := ln.File()
	ln.Close()
	if err == nil {
		defer lf.Close()
		err = os.Chmod(sock, 0o666)
	}
	if err != nil {
		end.Close()
		t.Fatal(err)
	}
	f.relay = exec.Command(self)
	f.relay.Env = []string{relayEnv + "=1", "GORACE=atexit_sleep_ms=0"}
	f.relay.ExtraFiles = []*os.File{relayEnd, lf} // processshim.RelayBrokerFD and RelayListenerFD
	f.relay.Stderr = os.Stderr
	if f.uid != os.Getuid() {
		f.relay.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(f.uid), Gid: uint32(f.gid)}}
	}
	if err := f.relay.Start(); err != nil {
		end.Close()
		t.Fatal(err)
	}
	return end
}

// waitRelay waits for the relay, which exits once the broker's connection
// ends.
func (f *fixture) waitRelay(t *testing.T) {
	done := make(chan error, 1)
	go func() { done <- f.relay.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("relay: %v", err)
		}
	case <-time.After(10 * time.Second):
		f.relay.Process.Kill()
		<-done
		t.Error("the relay still runs 10s after the broker closed")
	}
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
	if n := f.dials.Add(1); f.wrap != nil {
		return f.wrap(n, c), nil
	}
	return c, nil
}

// first applies wrap to the first stream alone.
func first(wrap func(net.Conn) io.ReadWriteCloser) func(int32, net.Conn) io.ReadWriteCloser {
	return func(n int32, c net.Conn) io.ReadWriteCloser {
		if n == 1 {
			return wrap(c)
		}
		return c
	}
}

// verdict is what intercept does with a request from the broker.
type verdict int

const (
	pass         verdict = iota // pass it to the service
	refuseBusy                  // answer Busy with no effect, as a service at its request limit does
	loseResponse                // pass it on, then lose the stream in place of its response
)

// intercept relays a stream between the broker and the service and applies
// decide to each request.
func intercept(svc net.Conn, decide func(sandboxwire.Frame) verdict) io.ReadWriteCloser {
	broker, relay := net.Pipe()
	var mu sync.Mutex // writes to the broker
	var lost atomic.Uint64
	cut := func() {
		svc.Close()
		relay.Close()
	}
	go func() {
		defer cut()
		for {
			fr, err := sandboxwire.ReadFrame(svc, sandboxwire.MaxPayload)
			if err != nil || fr.RequestID != 0 && fr.RequestID == lost.Load() {
				return
			}
			mu.Lock()
			err = sandboxwire.WriteFrame(relay, fr)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer cut()
		for {
			fr, err := sandboxwire.ReadFrame(relay, sandboxwire.MaxPayload)
			if err != nil {
				return
			}
			switch decide(fr) {
			case refuseBusy:
				m := sp.ResponseFailure{Request: fr.Type, Failure: *sp.Fail(sp.CodeBusy, sandboxwire.EffectNone, "busy")}
				mu.Lock()
				err = sandboxwire.WriteFrame(relay, sandboxwire.Frame{Type: m.MessageType(), RequestID: fr.RequestID, Payload: sp.Encode(m)})
				mu.Unlock()
			case loseResponse:
				lost.Store(fr.RequestID)
				fallthrough
			default:
				err = sandboxwire.WriteFrame(svc, fr)
			}
			if err != nil {
				return
			}
		}
	}()
	return broker
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

// holdEvents holds the service's events until the scope closes, then passes
// them on at once and loses the link before anything that follows.
type holdEvents struct {
	net.Conn
	held, out bytes.Buffer
	cut       bool
}

func (c *holdEvents) Read(p []byte) (int, error) {
	for c.out.Len() == 0 {
		if c.cut {
			c.Conn.Close()
			return 0, net.ErrClosed
		}
		fr, err := sandboxwire.ReadFrame(c.Conn, sandboxwire.MaxPayload)
		if err != nil {
			return 0, err
		}
		dst := &c.out
		if fr.RequestID == 0 {
			dst = &c.held
		}
		if err := sandboxwire.WriteFrame(dst, fr); err != nil {
			return 0, err
		}
		if fr.Type == sp.EventScopeClosed {
			c.held.WriteTo(&c.out)
			c.cut = true
		}
	}
	return c.out.Read(p)
}

// busyAck answers the first acknowledgement with Busy after the service
// applied it.
type busyAck struct {
	net.Conn
	out  bytes.Buffer
	sent bool
}

func (c *busyAck) Read(p []byte) (int, error) {
	for c.out.Len() == 0 {
		fr, err := sandboxwire.ReadFrame(c.Conn, sandboxwire.MaxPayload)
		if err != nil {
			return 0, err
		}
		if !c.sent && fr.Type == sandboxwire.ResponseType(sp.OpAckEvents) {
			c.sent = true
			fr.Payload = sp.Encode(sp.ResponseFailure{Request: sp.OpAckEvents, Failure: *sp.Fail(sp.CodeBusy, sandboxwire.EffectNone, "busy")})
		}
		if err := sandboxwire.WriteFrame(&c.out, fr); err != nil {
			return 0, err
		}
	}
	return c.out.Read(p)
}

type countWriter struct{ n int }

func (w *countWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
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

// await polls done until it holds, for at most 10s.
func await(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !done(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
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

func build(out, pkg string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %v\n%s", pkg, err, msg)
	}
	return nil
}
