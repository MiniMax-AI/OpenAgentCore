package gateway

import (
	"bufio"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

const wait = 5 * time.Second

// loopback is a gateway served on host loopback listeners at free ports, as
// Start serves it in the Session's namespace.
type loopback struct {
	Endpoints
	// end ends the Session.
	end context.CancelFunc
	// handlers counts the requests being handled.
	handlers sync.WaitGroup
}

// serveOnLoopback serves cfg until the test ends.
func serveOnLoopback(t *testing.T, cfg Config) *loopback {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	g, err := build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	l := &loopback{end: cancel}
	lns := make([]*net.TCPListener, len(g.listeners))
	ports := make([]int, len(lns))
	for i := range lns {
		if lns[i], err = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}); err != nil {
			t.Fatal(err)
		}
		ports[i] = lns[i].Addr().(*net.TCPAddr).Port
		h := g.listeners[i].handler
		g.listeners[i].handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l.handlers.Add(1)
			defer l.handlers.Done()
			h.ServeHTTP(w, r)
		})
	}
	g.serve(ctx, lns)
	l.Endpoints = g.endpoints(ports)
	return l
}

// trust returns roots that trust srv.
func trust(srv *httptest.Server) *x509.CertPool {
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return roots
}

// noRedirects is a Harness-side client that shows each answer as it is.
var noRedirects = &http.Client{
	Transport:     &http.Transport{Proxy: nil},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       wait,
}

// sandbox serves the Network protocol behind a test relay, as oac-sandbox-io
// does, and counts the connections it makes.
type sandbox struct {
	open  func(context.Context) (sandboxlink.Stream, error)
	dials atomic.Int32
}

func (s *sandbox) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func (s *sandbox) Dial(ctx context.Context, addr netip.AddrPort) (*net.TCPConn, error) {
	s.dials.Add(1)
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		return nil, err
	}
	return c.(*net.TCPConn), nil
}

func startSandbox(t *testing.T) *sandbox {
	t.Helper()
	auth := sandboxlinktest.NewAuthority()
	srv := sandboxlinktest.StartRelay(t, relay.Config{Authority: auth})
	resource := sandboxlink.ResourceRef{TenantID: sandboxwire.NewID(), EnvironmentID: sandboxwire.NewID(),
		Kind: sandboxlink.ResourceAllocation, ID: sandboxwire.NewID(), Generation: 1}
	auth.AddServe([]byte("serve credential"), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: resource})

	sb := &sandbox{}
	connected := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sandboxlink.Serve(ctx, sandboxlink.ServeConfig{URL: srv.URL, TLS: srv.TLS, Credential: []byte("serve credential"),
			Resource: resource, ServerInstanceID: sandboxwire.NewID(),
			Services: []sandboxlink.ServiceHandler{{Service: sandboxlink.ServiceNetwork, Version: sandboxnet.Version,
				Serve: func(ctx context.Context, b sandboxlink.Bind, _ uint64, s sandboxlink.Stream) {
					sandboxnet.Serve(ctx, s, b.Egress, sb)
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
	select {
	case <-connected:
	case <-time.After(wait):
		t.Fatal("the sandbox did not connect to the relay")
	}

	runtimeID := sandboxwire.NewID()
	auth.AddRuntime([]byte("runtime credential"), runtimeID)
	link, err := sandboxlink.DialAttach(context.Background(), sandboxlink.AttachConfig{URL: srv.URL, TLS: srv.TLS,
		RuntimeID: runtimeID, Credential: []byte("runtime credential")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Close() })
	session, assignment := sandboxwire.NewID(), sandboxwire.NewID()
	sb.open = func(ctx context.Context) (sandboxlink.Stream, error) {
		grant, attachment := sandboxwire.NewID(), sandboxwire.NewID()
		auth.AddGrant(grant[:], sandboxlinktest.Grant{RuntimeID: runtimeID, Resource: resource, SessionID: session, AssignmentID: assignment,
			AssignmentEpoch: 1, Services: []sandboxlink.Service{sandboxlink.ServiceNetwork}, Lease: time.Minute})
		s, _, err := link.OpenService(ctx, sandboxlink.Open{Service: sandboxlink.ServiceNetwork, Version: sandboxnet.Version, Resource: resource,
			AttachmentID: attachment, SessionID: session, AssignmentID: assignment, AssignmentEpoch: 1, AttachGrant: grant[:]})
		return s, err
	}
	return sb
}

func TestSessionEndEndsBlockedRelays(t *testing.T) {
	// The upstream upgrades the connection and writes until the Harness's
	// side is full, then reads until the gateway closes its side.
	flooded, closed := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		rw.Flush()
		chunk := make([]byte, 64<<10)
		for {
			c.SetWriteDeadline(time.Now().Add(time.Second))
			if _, err := c.Write(chunk); err != nil {
				break
			}
		}
		close(flooded)
		c.SetWriteDeadline(time.Time{})
		io.Copy(io.Discard, c)
		close(closed)
	}))
	defer srv.Close()
	gw := serveOnLoopback(t, Config{
		MCP:    []agent.MCPBinding{{ConnectionOrigin: "service", ServerLabel: "tools", Transport: "http", ServerURL: srv.URL + "/mcp"}},
		Prompt: proto.PromptRequestPayload{DisableExecutionEnvironment: true},
	})

	u, _ := url.Parse(gw.MCP["tools"])
	harness, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer harness.Close()
	fmt.Fprintf(harness, "GET /mcp HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n", u.Host)
	if resp, err := http.ReadResponse(bufio.NewReader(harness), nil); err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	// The Harness reads nothing more, so the gateway's write to it blocks.
	select {
	case <-flooded:
	case <-time.After(2 * wait):
		t.Fatal("the upstream never filled the Harness's side")
	}

	gw.end()
	returned := make(chan struct{})
	go func() {
		gw.handlers.Wait()
		close(returned)
	}()
	for what, done := range map[string]chan struct{}{"the relay": returned, "the upstream connection": closed} {
		select {
		case <-done:
		case <-time.After(wait):
			t.Fatalf("%s is still open after the Session ended", what)
		}
	}
}

func TestRejectedUpgradeClosesTheUpstream(t *testing.T) {
	// The upstream switches to a protocol the request did not ask for.
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: other\r\n\r\n")
		rw.Flush()
		io.Copy(io.Discard, c)
		close(closed)
	}))
	defer srv.Close()
	gw := serveOnLoopback(t, Config{
		MCP:    []agent.MCPBinding{{ConnectionOrigin: "service", ServerLabel: "tools", Transport: "http", ServerURL: srv.URL + "/mcp"}},
		Prompt: proto.PromptRequestPayload{DisableExecutionEnvironment: true},
	})

	req, _ := http.NewRequest("GET", gw.MCP["tools"], nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "test")
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("answer %d", resp.StatusCode)
	}
	select {
	case <-closed:
	case <-time.After(wait):
		t.Fatal("the upstream connection is still open")
	}
}
