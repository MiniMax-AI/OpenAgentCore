package sandboxnet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
)

// heldStream is a Stream whose writes wait until release is closed, so a test
// can hold Serve's answer on its way out.
type heldStream struct {
	in      *io.PipeReader
	writing chan struct{} // closed when the first Write starts
	release chan struct{}
	once    sync.Once
}

func (h *heldStream) Read(b []byte) (int, error) { return h.in.Read(b) }
func (h *heldStream) Write(b []byte) (int, error) {
	h.once.Do(func() { close(h.writing) })
	<-h.release
	return len(b), nil
}
func (h *heldStream) CloseWrite() error                { return nil }
func (h *heldStream) Close() error                     { return h.in.Close() }
func (h *heldStream) Reset() error                     { return h.in.CloseWithError(errors.New("reset")) }
func (h *heldStream) SetDeadline(time.Time) error      { return nil }
func (h *heldStream) SetReadDeadline(time.Time) error  { return nil }
func (h *heldStream) SetWriteDeadline(time.Time) error { return nil }

// loopback dials IP literals and resolves nothing.
type loopback struct{}

func (loopback) Resolve(context.Context, string) ([]netip.Addr, error) {
	return nil, errors.New("no names")
}

func (loopback) Dial(ctx context.Context, addr netip.AddrPort) (*net.TCPConn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp4", addr.String())
	if err != nil {
		return nil, err
	}
	return c.(*net.TCPConn), nil
}

// Bytes that arrive while Connected is being written reach the destination
// only after the answer is out.
func TestForwardsOnlyAfterTheAnswer(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dst := ln.Addr().(*net.TCPAddr).AddrPort()
	in, feed := io.Pipe()
	s := &heldStream{in: in, writing: make(chan struct{}), release: make(chan struct{})}
	served := make(chan error, 1)
	egress := []sandboxlink.EgressRule{{Prefix: netip.PrefixFrom(dst.Addr(), 32), PortFirst: dst.Port(), PortLast: dst.Port()}}
	go func() { served <- Serve(context.Background(), s, egress, loopback{}) }()
	go WriteMessage(feed, 1, ConnectRequest{Network: NetworkTCP, Host: dst.Addr().String(), Port: dst.Port(), TimeoutMillis: 5000})

	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-s.writing:
	case <-time.After(5 * time.Second):
		t.Fatal("Connected was never written")
	}
	// The pipe returns once Serve has read the bytes.
	if _, err := feed.Write([]byte("early")); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, _ := c.Read(make([]byte, 8)); n != 0 {
		t.Fatal("bytes reached the destination before Connected was written")
	}
	close(s.release)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, 5)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "early" {
		t.Fatalf("destination read %q, %v", got, err)
	}
	feed.Close()
	c.Close()
	if err := <-served; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}
