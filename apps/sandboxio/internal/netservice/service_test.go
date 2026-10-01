//go:build linux

package netservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

const wait = 5 * time.Second

var (
	sessionID    = sandboxwire.NewID()
	assignmentID = sandboxwire.NewID()
	resource     = sandboxlink.ResourceRef{TenantID: sandboxwire.NewID(), EnvironmentID: sandboxwire.NewID(),
		Kind: sandboxlink.ResourceAllocation, ID: sandboxwire.NewID(), Generation: 1}
)

func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(wait):
		t.Fatalf("timed out waiting for %T", *new(T))
		panic("unreachable")
	}
}

// fixture runs this service as the network handler of a serve peer behind a
// test relay, with an attach link to open streams on. Its resolver asks a fake
// DNS server: echo.test is 127.0.0.1, blackhole.test never answers and every
// other name does not exist.
type fixture struct {
	t         *testing.T
	auth      *sandboxlinktest.Authority
	srv       *sandboxlinktest.Server
	link      *sandboxlink.AttachLink
	runtime   sandboxwire.ID
	served    chan error    // sandboxnet.Serve's result for each stream
	blackhole chan struct{} // a blackhole.test query arrived
}

func newFixture(t *testing.T) *fixture {
	auth := sandboxlinktest.NewAuthority()
	f := &fixture{t: t, auth: auth, srv: sandboxlinktest.StartRelay(t, relay.Config{Authority: auth}), runtime: sandboxwire.NewID(),
		served: make(chan error, 16), blackhole: make(chan struct{}, 16)}
	dns := f.startDNS()
	svc := &Service{resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", dns)
	}}}

	auth.AddServe([]byte("serve credential"), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: resource})
	connected := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sandboxlink.Serve(ctx, sandboxlink.ServeConfig{URL: f.srv.URL, TLS: f.srv.TLS, Credential: []byte("serve credential"),
			Resource: resource, ServerInstanceID: sandboxwire.NewID(),
			Services: []sandboxlink.ServiceHandler{{Service: sandboxlink.ServiceNetwork, Version: sandboxnet.Version,
				Serve: func(ctx context.Context, b sandboxlink.Bind, _ uint64, s sandboxlink.Stream) {
					f.served <- sandboxnet.Serve(ctx, s, b.Egress, svc)
				}}},
			OnConnected: func(sandboxlink.HelloAccepted) {
				select {
				case connected <- struct{}{}:
				default:
				}
			}})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	recv(t, connected)

	auth.AddRuntime([]byte("runtime credential"), f.runtime)
	link, err := sandboxlink.DialAttach(context.Background(), sandboxlink.AttachConfig{URL: f.srv.URL, TLS: f.srv.TLS,
		RuntimeID: f.runtime, Credential: []byte("runtime credential")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Close() })
	f.link = link
	return f
}

func (f *fixture) startDNS() string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply := f.answer(buf[:n]); reply != nil {
				pc.WriteTo(reply, from)
			}
		}
	}()
	return pc.LocalAddr().String()
}

func (f *fixture) answer(query []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	if strings.HasPrefix(q.Name.String(), "blackhole.test.") {
		select {
		case f.blackhole <- struct{}{}:
		default:
		}
		return nil
	}
	m := dnsmessage.Message{Header: dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionAvailable: true,
		RCode: dnsmessage.RCodeNameError}, Questions: []dnsmessage.Question{q}}
	if q.Name.String() == "echo.test." {
		m.RCode = dnsmessage.RCodeSuccess
		if q.Type == dnsmessage.TypeA {
			m.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
				Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
		}
	}
	reply, err := m.Pack()
	if err != nil {
		return nil
	}
	return reply
}

// open opens a Network stream of a new attachment whose egress is egress; nil
// allows everything.
func (f *fixture) open(egress []sandboxlink.EgressRule) (sandboxlink.Stream, sandboxwire.ID) {
	f.t.Helper()
	grant, attachment := sandboxwire.NewID(), sandboxwire.NewID()
	f.auth.AddGrant(grant[:], sandboxlinktest.Grant{RuntimeID: f.runtime, Resource: resource, SessionID: sessionID, AssignmentID: assignmentID,
		AssignmentEpoch: 1, Services: []sandboxlink.Service{sandboxlink.ServiceNetwork}, Lease: time.Minute, Egress: egress})
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	s, _, err := f.link.OpenService(ctx, sandboxlink.Open{Service: sandboxlink.ServiceNetwork, Version: sandboxnet.Version, Resource: resource,
		AttachmentID: attachment, SessionID: sessionID, AssignmentID: assignmentID, AssignmentEpoch: 1, AttachGrant: grant[:]})
	if err != nil {
		f.t.Fatal(err)
	}
	return s, attachment
}

func (f *fixture) connect(egress []sandboxlink.EgressRule, host string, port uint16, timeout time.Duration) (*sandboxnet.Conn, error) {
	s, _ := f.open(egress)
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	return sandboxnet.Connect(ctx, s, host, port, timeout)
}

func listen(t *testing.T) (*net.TCPListener, uint16) {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln, uint16(ln.Addr().(*net.TCPAddr).Port)
}

func random(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func TestHalfCloseFromEachSide(t *testing.T) {
	f := newFixture(t)
	ln, port := listen(t)
	fromServer, fromClient := random(256<<10), random(1<<20)
	received := make(chan []byte, 1)
	go func() {
		c, err := ln.AcceptTCP()
		if err != nil {
			received <- nil
			return
		}
		defer c.Close()
		c.Write(fromServer)
		c.CloseWrite()
		b, _ := io.ReadAll(c)
		received <- b
	}()

	conn, err := f.connect(nil, "echo.test", port, wait)
	if err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() {
		_, err := conn.Write(fromClient)
		if err == nil {
			err = conn.CloseWrite()
		}
		written <- err
	}()
	// The destination ended first; the other direction carries on.
	got, err := io.ReadAll(conn)
	if err != nil || !bytes.Equal(got, fromServer) {
		t.Fatalf("read %d bytes, %v; want the destination's %d bytes and EOF", len(got), err, len(fromServer))
	}
	if err := recv(t, written); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, received); !bytes.Equal(got, fromClient) {
		t.Fatalf("destination read %d bytes, want %d", len(got), len(fromClient))
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recv(t, f.served); err != nil {
		t.Fatalf("Serve: %v, want an orderly end", err)
	}
}

func TestDestinationResetAborts(t *testing.T) {
	f := newFixture(t)
	ln, port := listen(t)
	go func() {
		c, err := ln.AcceptTCP()
		if err != nil {
			return
		}
		c.Read(make([]byte, 1))
		c.SetLinger(0)
		c.Close()
	}()
	conn, err := f.connect(nil, "echo.test", port, wait)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(conn); err == nil {
		t.Fatal("read ended with EOF, want an abort")
	}
	if err := recv(t, f.served); err == nil {
		t.Fatal("Serve ended in order, want an abort")
	}
}

// Concurrent writes and reads each arrive whole, and a passed read deadline
// fails a Read even with input buffered.
func TestConcurrentCallsAndDeadline(t *testing.T) {
	f := newFixture(t)
	ln, port := listen(t)
	go func() {
		if c, err := ln.Accept(); err == nil {
			io.Copy(c, c)
			c.Close()
		}
	}()
	conn, err := f.connect(nil, "127.0.0.1", port, wait)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Writers race one another while a reader drains the echo.
	const writers, size = 4, 256 << 10
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			if _, err := conn.Write(random(size)); err != nil {
				t.Error(err)
			}
		})
	}
	if _, err := io.ReadFull(conn, make([]byte, writers*size)); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if _, err := conn.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(conn, b); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(-time.Second))
	if _, err := conn.Read(b); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read after the deadline: %v, want os.ErrDeadlineExceeded", err)
	}
	conn.SetReadDeadline(time.Time{})
	if _, err := io.ReadFull(conn, b); err != nil || b[0] != 'b' {
		t.Fatalf("read after clearing the deadline: %q, %v", b, err)
	}
}

func TestConnectFailures(t *testing.T) {
	f := newFixture(t)
	// The destination listens on every local address, so a dial to an
	// unspecified address, which means this host, would reach it.
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	open := uint16(ln.Addr().(*net.TCPAddr).Port)
	refused, closedPort := listen(t)
	refused.Close()
	only := func(prefix string) []sandboxlink.EgressRule {
		return []sandboxlink.EgressRule{{Prefix: netip.MustParsePrefix(prefix), PortFirst: 1, PortLast: 65535}}
	}
	for _, tc := range []struct {
		name    string
		egress  []sandboxlink.EgressRule
		host    string
		port    uint16
		timeout time.Duration
		code    sandboxnet.Code
	}{
		{"denied", only("10.0.0.0/8"), "echo.test", open, wait, sandboxnet.CodeDenied},
		{"unspecified IPv6", only("::/0"), "::", open, wait, sandboxnet.CodeDenied},
		{"unspecified IPv4", only("0.0.0.0/8"), "0.0.0.0", open, wait, sandboxnet.CodeDenied},
		{"not resolved", nil, "missing.test", open, wait, sandboxnet.CodeNameNotResolved},
		{"refused", nil, "127.0.0.1", closedPort, wait, sandboxnet.CodeConnectionRefused},
		{"timed out", nil, "blackhole.test", open, 200 * time.Millisecond, sandboxnet.CodeTimedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := f.connect(tc.egress, tc.host, tc.port, tc.timeout)
			var e *sandboxnet.Error
			if !errors.As(err, &e) || e.Code != tc.code || e.Effect != sandboxwire.EffectNone {
				t.Fatalf("Connect: %v, %v; want %s with no effect", conn, err, tc.code)
			}
			if err := recv(t, f.served); !errors.Is(err, tc.code) {
				t.Fatalf("Serve: %v, want %s", err, tc.code)
			}
		})
	}
	// No case may have dialed the listener.
	ln.SetDeadline(time.Now().Add(100 * time.Millisecond))
	if c, err := ln.Accept(); err == nil {
		c.Close()
		t.Fatal("a refused Connect reached the destination")
	}
}

func TestSecondConnectIsViolation(t *testing.T) {
	f := newFixture(t)
	s, _ := f.open(nil)
	var frames bytes.Buffer
	req := sandboxnet.ConnectRequest{Network: sandboxnet.NetworkTCP, Host: "blackhole.test", Port: 80, TimeoutMillis: 10_000}
	sandboxnet.WriteMessage(&frames, 1, req)
	sandboxnet.WriteMessage(&frames, 2, req)
	if _, err := s.Write(frames.Bytes()); err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(s); err == nil || len(got) != 0 {
		t.Fatalf("read %d bytes, %v; want an abort without an answer", len(got), err)
	}
	if err := recv(t, f.served); !errors.Is(err, sandboxnet.ErrProtocolViolation) {
		t.Fatalf("Serve: %v, want a protocol violation", err)
	}
}

func TestLostAnswerIsUncertain(t *testing.T) {
	f := newFixture(t)
	s, attachment := f.open(nil)
	result := make(chan error, 1)
	go func() {
		_, err := sandboxnet.Connect(context.Background(), s, "blackhole.test", 80, 10*time.Second)
		result <- err
	}()
	recv(t, f.blackhole)
	f.srv.Relay.RevokeAttachment(attachment)
	var e *sandboxnet.Error
	if err := recv(t, result); !errors.As(err, &e) || e.Effect != sandboxwire.EffectPossible {
		t.Fatalf("Connect: %v, want a failure with a possible effect", err)
	}
}
