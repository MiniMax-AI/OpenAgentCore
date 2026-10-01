package processshim

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func connPair(t *testing.T) (*Conn, *Conn) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.DialUnix("unix", nil, ln.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	server, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return NewConn(client), NewConn(server)
}

func pipeFDs(t *testing.T) ([3]int, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	fd := int(w.Fd())
	return [3]int{fd, fd, fd}, r
}

func TestRequestCarriesCloseOnExecDescriptors(t *testing.T) {
	shim, broker := connPair(t)
	fds, r := pipeFDs(t)
	req := fixtures[0].msg.(Request)
	if err := shim.SendRequest(req, fds); err != nil {
		t.Fatal(err)
	}
	got, recv, err := broker.ReadRequest()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("request %#v, want %#v", got, req)
	}
	for _, fd := range recv {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("fd %d flags %#x: %v", fd, flags, err)
		}
	}
	if _, err := unix.Write(recv[1], []byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := r.Read(buf); err != nil || buf[0] != 'x' {
		t.Fatalf("read %q: %v", buf, err)
	}
	closeAll(recv[:])
}

func TestUnexpectedDescriptorsAreRejected(t *testing.T) {
	t.Run("request without descriptors", func(t *testing.T) {
		shim, broker := connPair(t)
		if err := shim.Send(fixtures[0].msg); err != nil {
			t.Fatal(err)
		}
		if _, _, err := broker.ReadRequest(); !errors.Is(err, ErrProtocol) {
			t.Fatalf("got %v, want ErrProtocol", err)
		}
	})
	t.Run("descriptors on a later message", func(t *testing.T) {
		shim, broker := connPair(t)
		fds, _ := pipeFDs(t)
		if err := shim.SendRequest(fixtures[0].msg.(Request), fds); err != nil {
			t.Fatal(err)
		}
		_, recv, err := broker.ReadRequest()
		if err != nil {
			t.Fatal(err)
		}
		closeAll(recv[:])
		frame := Frame(Signal{Number: 2})
		var buf frameBuffer
		_ = sandboxwire.WriteFrame(&buf, frame)
		if _, _, err := shim.c.WriteMsgUnix(buf, unix.UnixRights(fds[0]), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := broker.ReadMessage(); !errors.Is(err, ErrProtocol) {
			t.Fatalf("got %v, want ErrProtocol", err)
		}
	})
}
