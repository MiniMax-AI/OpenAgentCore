package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

const wait = 5 * time.Second

// serveOnLoopback serves cfg on host loopback listeners at free ports, as
// Start serves it in the Session's namespace, until the test ends.
func serveOnLoopback(t *testing.T, cfg Config) Endpoints {
	t.Helper()
	g, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	lns := make([]net.Listener, len(g.listeners))
	ports := make([]int, len(lns))
	for i := range lns {
		if lns[i], err = net.Listen("tcp4", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		ports[i] = lns[i].Addr().(*net.TCPAddr).Port
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	g.serve(ctx, lns)
	return g.endpoints(ports)
}

// trust returns a TLS configuration that trusts srv.
func trust(srv *httptest.Server) *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: roots}
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
				Serve: func(ctx context.Context, b sandboxlink.Bind, s sandboxlink.Stream) {
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
