//go:build linux

package processshim

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// testRelay runs a relay in this process with its broker's end at broker.
type testRelay struct {
	sock   string
	broker *net.UnixConn
	in     *bufio.Reader
}

// startTestRelay starts a relay that configure may give test seams.
func startTestRelay(t *testing.T, configure func(*relay)) *testRelay {
	t.Helper()
	sv, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	ends := [2]*net.UnixConn{}
	for i, fd := range sv {
		f := os.NewFile(uintptr(fd), "broker")
		c, err := net.FileConn(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		ends[i] = c.(*net.UnixConn)
	}
	sock := filepath.Join(t.TempDir(), SocketName)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	r := &relay{broker: ends[1], ln: ln, invs: map[uint64]*invocation{}}
	if configure != nil {
		configure(r)
	}
	done := make(chan struct{})
	go r.accept()
	go func() {
		defer close(done)
		r.serveBroker()
		r.lose()
		r.broker.Close()
	}()
	t.Cleanup(func() {
		ends[0].Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the relay still runs after its broker left")
		}
	})
	return &testRelay{sock: sock, broker: ends[0], in: bufio.NewReader(ends[0])}
}

// read returns the relay's next message to the broker.
func (r *testRelay) read() (RelayMessage, error) {
	f, err := sandboxwire.ReadFrame(r.in, MaxFrameBytes)
	if err != nil {
		return nil, err
	}
	return DecodeRelay(f)
}

func (r *testRelay) send(ms ...BrokerMessage) error {
	for _, m := range ms {
		if err := sandboxwire.WriteFrame(r.broker, Frame(m)); err != nil {
			return err
		}
	}
	return nil
}

// shim hands the relay an invocation of name with fds, as the shim does.
func (r *testRelay) shim(t *testing.T, name string, fds [3]int) *Conn {
	t.Helper()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: r.sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	conn := NewConn(c)
	t.Cleanup(func() { conn.Close() })
	req := Request{Version: Version, ExecPath: []byte("/.oac/bin/" + name), Argv: [][]byte{[]byte(name)}, Env: [][]byte{}, Cwd: []byte("/"), Umask: 0o022}
	if err := conn.SendRequest(req, fds); err != nil {
		t.Fatal(err)
	}
	return conn
}

// finished reads the shim's Ack and Result.
func finished(conn *Conn) (Result, error) {
	m, err := conn.ReadMessage()
	if err != nil {
		return Result{}, err
	}
	if _, ok := m.(Ack); !ok {
		return Result{}, fmt.Errorf("got %#v before the Ack", m)
	}
	m, err = conn.ReadMessage()
	if err != nil {
		return Result{}, err
	}
	res, ok := m.(Result)
	if !ok {
		return Result{}, fmt.Errorf("got %#v, want a Result", m)
	}
	return res, nil
}

func devNull(t *testing.T) [3]int {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	fd := int(f.Fd())
	return [3]int{fd, fd, fd}
}

// While one invocation is between its ID and its Open, another handshake
// waits for both steps, so the broker sees the Opens with increasing IDs, as
// it requires.
func TestOpensReachTheBrokerInIDOrder(t *testing.T) {
	paused, release := make(chan struct{}), make(chan struct{})
	r := startTestRelay(t, func(r *relay) {
		r.publishing = func(req Request) {
			if string(req.Argv[0]) == "first" {
				close(paused)
				<-release
			}
		}
	})
	broken := make(chan error, 1)
	secondOpened := make(chan struct{})
	go func() {
		var last uint64
		for {
			m, err := r.read()
			if err != nil {
				return
			}
			open, ok := m.(Open)
			if !ok {
				continue
			}
			if string(open.Request.Argv[0]) == "second" {
				close(secondOpened)
			}
			if open.ID <= last {
				// The broker ends the Session at an Open ID that does not
				// increase.
				broken <- fmt.Errorf("Open %d after %d", open.ID, last)
				r.broker.Close()
				return
			}
			last = open.ID
			id := open.ID
			r.send(Accept{ID: id}, Started{ID: id}, Exit{ID: id, Result: Result{Code: 0}}, End{ID: id})
		}
	}()
	first := r.shim(t, "first", devNull(t))
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("the first handshake never reached publication")
	}
	// The second handshake either waits for the first's publication or, if
	// the steps were apart, publishes its own Open first.
	second := r.shim(t, "second", devNull(t))
	settled := func() bool {
		select {
		case <-secondOpened:
			return true
		default:
			return waitsForLock("processshim.(*relay).open(")
		}
	}
	for deadline := time.Now().Add(10 * time.Second); !settled(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the second handshake neither waited nor published")
		}
	}
	close(release)
	for name, conn := range map[string]*Conn{"first": first, "second": second} {
		if res, err := finished(conn); err != nil || res.Code != 0 {
			t.Errorf("%s: Result %+v, %v", name, res, err)
		}
	}
	select {
	case err := <-broken:
		t.Fatal(err)
	default:
	}
}

// waitsForLock reports whether a goroutine in fn waits to lock a mutex.
func waitsForLock(fn string) bool {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "[sync.Mutex.Lock") && strings.Contains(g, fn) {
			return true
		}
	}
	return false
}

// Output for a terminal is written only once the terminal is raw, so the
// local terminal never processes the remote terminal's output again, even
// when the output reaches its pump before the control goroutine takes
// Started.
func TestTerminalOutputWaitsForRawMode(t *testing.T) {
	ptm, pts, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer ptm.Close()
	defer pts.Close()
	fd := int(pts.Fd())
	tio, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	tio.Oflag |= unix.OPOST | unix.ONLCR // cooked output turns \n into \r\n
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, tio); err != nil {
		t.Fatal(err)
	}
	written := make(chan struct{})
	r := startTestRelay(t, func(r *relay) {
		// Hold the control goroutine in Started until the output is
		// written, or long enough for a pump that does not wait to write it.
		r.makingRaw = func() {
			select {
			case <-written:
			case <-time.After(300 * time.Millisecond):
			}
		}
	})
	const out = "a\r\nb\r\n" // the remote terminal's output
	go func() {
		for {
			m, err := r.read()
			if err != nil {
				return
			}
			switch m := m.(type) {
			case Open:
				id := m.ID
				r.send(Accept{ID: id}, Started{ID: id}, Output{ID: id, FD: 1, Seq: 1, Data: []byte(out)})
			case Written:
				close(written)
				r.send(Exit{ID: m.ID, Result: Result{Code: 0}, Marks: []Mark{{FD: 1, Seq: 1}}}, End{ID: m.ID})
			}
		}
	}()
	conn := r.shim(t, "sh", [3]int{fd, fd, fd})
	if res, err := finished(conn); err != nil || res.Code != 0 {
		t.Fatalf("Result %+v, %v", res, err)
	}
	if got := drain(t, int(ptm.Fd())); string(got) != out {
		t.Fatalf("the terminal got %q, want %q", got, out)
	}
}

// drain reads what the terminal's master side holds until it stays empty
// for 100ms.
func drain(t *testing.T, fd int) []byte {
	t.Helper()
	var got []byte
	buf := make([]byte, 256)
	for {
		n, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 100)
		switch {
		case err == unix.EINTR:
			continue
		case err != nil:
			t.Fatal(err)
		case n == 0:
			return got
		}
		m, err := unix.Read(fd, buf)
		if err != nil || m == 0 {
			return got
		}
		got = append(got, buf[:m]...)
	}
}
