//go:build linux

package processshim

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// An invocation whose handshake finishes first is still published after one
// that reached the relay earlier, and the broker sees their Opens with
// increasing IDs, as it requires.
func TestOpensReachTheBrokerInIDOrder(t *testing.T) {
	paused := make(chan struct{})
	secondOpened := make(chan struct{})
	r := startTestRelay(t, func(r *relay) {
		r.publishing = func(req Request) {
			if string(req.Argv[0]) == "first" {
				close(paused)
				<-secondOpened
			}
		}
	})
	broken := make(chan error, 1)
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
			if string(open.Request.Argv[0]) == "second" {
				close(secondOpened)
			}
		}
	}()
	first := r.shim(t, "first", devNull(t))
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("the first handshake never reached publication")
	}
	second := r.shim(t, "second", devNull(t))
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
