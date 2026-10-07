// Package gateway is the Session gateway on the agent host. Inside the
// Session's loopback-only network namespace it serves one listener for the
// frozen model upstream, one per MCP HTTP binding and, when the view has one, a
// generic proxy, so the Harness never holds an upstream credential and has no
// network route of its own.
//
// A listener's identity selects its upstream and credential; nothing is routed
// by hostname. The model listener relays the declared native routes of its
// protocol (internal/modelprovider) to the upstream from the agent host and
// injects the credential. An MCP listener relays to its binding's server and
// injects the binding's bearer token and HTTP headers: an environment-origin
// binding connects through the sandbox's Network service, a service-origin
// binding from the agent host, and the gateway does the TLS either way. An MCP
// listener serves only its server URL's path, without a query, and relays to
// exactly the server URL. The server URL's query is a credential: a binding
// with a query or an injected value needs https. The generic proxy carries
// HTTP CONNECT tunnels and plain-HTTP forward requests, and connects only
// through the sandbox's Network service. Redirects reach the Harness unchanged
// and are never followed. Response header and trailer values that contain an
// injected credential, header value or MCP query value are withheld; bodies
// pass unchanged. Stopping the gateway closes every connection, tunnels and
// upgraded ones included. The gateway logs nothing.
package gateway

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
)

// Config is what one Session's gateway serves. It holds credentials: keep it
// in memory and never log it.
type Config struct {
	// Model is the Session's frozen model upstream.
	Model modelprovider.Provider
	// MCP are the Session's effective MCP bindings as
	// agent.ResolveMCPBindings returns them, bearer tokens and HTTP headers
	// included. Each is an HTTP binding, and server labels are unique.
	MCP []agent.MCPBinding
	// Prompt is the request the bindings were resolved from. Each binding's
	// origin is admitted against its placement with
	// proto.MCPHTTPServer.ValidateConnectionOrigin before anything else.
	Prompt proto.PromptRequestPayload
	// OpenNetwork opens a new Network stream to the Session's sandbox, as
	// sandboxnet.Connect takes it. Nil means the Session has no sandbox
	// network: the generic proxy then refuses every request, and an
	// environment-origin binding is invalid.
	OpenNetwork func(context.Context) (sandboxlink.Stream, error)
	// RootCAs are the roots the gateway trusts for upstream TLS. Nil means
	// the system roots. The server name is always the destination's hostname.
	RootCAs *x509.CertPool
	// Proxy serves the generic proxy at ProxyPort. Without it nothing listens
	// there and Endpoints.Proxy is empty; the other listeners keep their
	// ports.
	Proxy bool
}

// Endpoints is what the Harness is given in place of upstreams and
// credentials. Every URL is plain HTTP on the Session's loopback.
type Endpoints struct {
	// Model is the model listener's base URL, http://127.0.0.1:<port>, with
	// no path. The listener adds the frozen base URL's path.
	Model string
	// MCP maps each binding's server label to the URL the Harness uses: its
	// listener with the server URL's path and no query.
	MCP map[string]string
	// Proxy is the generic proxy's URL, for HTTP and HTTPS proxy settings,
	// or empty when Config.Proxy is unset.
	Proxy string
}

// ProxyPort is the generic proxy's port in the Session's namespace. The model
// listener takes the following port, then the MCP listeners in Config order,
// whether or not the proxy is served.
// The namespace is the Session's own and the gateway listens before the
// Harness starts, so the ports are free; fixing them lets the Harness's
// environment be built before the namespace exists.
const ProxyPort = 17100

// maxListeners bounds the listeners of one Session.
const maxListeners = 256

var (
	// ErrInvalidConfig is a Config that Plan and Start reject. The message
	// names the item by its label and never includes a credential.
	ErrInvalidConfig = errors.New("gateway: invalid configuration")
	// ErrNetwork is a failure to listen in the Session's network namespace.
	ErrNetwork = errors.New("gateway: session network")
	// ErrUnsupported is returned by Start outside Linux.
	ErrUnsupported = errors.New("gateway: unsupported platform")
)

// Plan validates cfg and returns the Endpoints that Start serves for it, so
// the Harness's environment can be built before the view starts.
func Plan(cfg Config) (Endpoints, error) {
	g, err := build(context.Background(), cfg)
	if err != nil {
		return Endpoints{}, err
	}
	return g.endpoints(g.fixedPorts()), nil
}

// Start validates cfg, opens its listeners inside the network namespace ns at
// the Endpoints of Plan(cfg) and serves them from the daemon until stop is
// called. It is meant to run in sessionview's network hook; nothing listens
// outside the namespace. stop ends every request, and closes the listeners
// and every connection before it returns.
func Start(ns *os.File, cfg Config) (stop func(), err error) {
	ctx, cancel := context.WithCancel(context.Background())
	g, err := build(ctx, cfg)
	var lns []*net.TCPListener
	if err == nil {
		lns, err = listen(ns, g.fixedPorts())
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return g.serve(ctx, cancel, lns), nil
}

// fixedPorts returns each listener's port: ProxyPort for the proxy, then the
// following ports in listener order.
func (g *gateway) fixedPorts() []int {
	first := ProxyPort + 1
	if len(g.listeners) > 0 && g.listeners[0].role == roleProxy {
		first = ProxyPort
	}
	ports := make([]int, len(g.listeners))
	for i := range ports {
		ports[i] = first + i
	}
	return ports
}

type role uint8

const (
	roleProxy role = iota
	roleModel
	roleMCP
)

// listener is one planned listener: what it serves and how the Harness
// addresses it.
type listener struct {
	role    role
	label   string // MCP: the server label
	suffix  string // MCP: the server URL's path
	handler http.Handler
}

type gateway struct {
	listeners []listener // the proxy when served, the model, then MCP in Config order
	conns     *connSet   // every connection, accepted or dialed
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidConfig}, args...)...)
}

// build validates cfg and makes each listener's handler. Every upstream dial
// ends with session, and every upstream connection joins g.conns.
func build(session context.Context, cfg Config) (*gateway, error) {
	if n := 2 + len(cfg.MCP); n > maxListeners {
		return nil, invalid("%d listeners, at most %d", n, maxListeners)
	}
	g := &gateway{conns: &connSet{open: map[*trackedConn]struct{}{}}}
	host := relayTransport(cfg.RootCAs, sessionDial(session, g.conns, hostDial))
	// The proxy tunnels with the sandbox dial and forwards on its own
	// transport; environment-origin MCP relays use a relay transport.
	var dial dialFunc
	var forward, sandbox *http.Transport
	if cfg.OpenNetwork != nil {
		dial = sessionDial(session, g.conns, sandboxDialer(cfg.OpenNetwork))
		forward, sandbox = newTransport(cfg.RootCAs, dial), relayTransport(cfg.RootCAs, dial)
	}
	if cfg.Proxy {
		g.listeners = append(g.listeners, listener{role: roleProxy, handler: newProxy(dial, forward)})
	}
	model, err := newModelRelay(cfg.Model, host)
	if err != nil {
		return nil, invalid("model upstream: %v", err)
	}
	g.listeners = append(g.listeners, listener{role: roleModel, handler: model})

	labels := map[string]bool{}
	for _, b := range cfg.MCP {
		if err := (proto.MCPHTTPServer{ConnectionOrigin: b.ConnectionOrigin}).ValidateConnectionOrigin(cfg.Prompt); err != nil {
			return nil, invalid("MCP server %q: %v", b.ServerLabel, err)
		}
		if b.ServerLabel == "" || labels[b.ServerLabel] {
			return nil, invalid("MCP server label %q is empty or repeated", b.ServerLabel)
		}
		labels[b.ServerLabel] = true
		if b.Transport != "http" {
			return nil, invalid("MCP server %q has transport %q, not http", b.ServerLabel, b.Transport)
		}
		transport := host
		if b.ConnectionOrigin == "environment" {
			if sandbox == nil {
				return nil, invalid("MCP server %q has environment origin and the Session has no sandbox network", b.ServerLabel)
			}
			transport = sandbox
		}
		h, suffix, err := newMCPRelay(b, transport)
		if err != nil {
			return nil, invalid("MCP server %q: %v", b.ServerLabel, err)
		}
		g.listeners = append(g.listeners, listener{role: roleMCP, label: b.ServerLabel, suffix: suffix, handler: h})
	}
	return g, nil
}

func (g *gateway) endpoints(ports []int) Endpoints {
	e := Endpoints{MCP: map[string]string{}}
	for i, l := range g.listeners {
		base := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[i]))
		switch l.role {
		case roleProxy:
			e.Proxy = base
		case roleModel:
			e.Model = base
		case roleMCP:
			e.MCP[l.label] = base + l.suffix
		}
	}
	return e
}

// quiet discards what net/http would log: a logged request or upstream error
// could carry more than the gateway chooses to reveal.
var quiet = log.New(io.Discard, "", 0)

// serve serves each listener with its handler and returns its stop. stop
// cancels ctx, which ends every request and dial, closes the listeners, waits
// until none is accepting and then resets every connection either way, which
// ends a relay blocked on a peer that does not read. It returns once all of
// them have closed.
func (g *gateway) serve(ctx context.Context, cancel context.CancelFunc, lns []*net.TCPListener) (stop func()) {
	var accepting sync.WaitGroup
	for i, ln := range lns {
		srv := &http.Server{
			Handler:           g.listeners[i].handler,
			ReadHeaderTimeout: 30 * time.Second,
			ErrorLog:          quiet,
			BaseContext:       func(net.Listener) context.Context { return ctx },
		}
		accepting.Go(func() { srv.Serve(sessionListener{TCPListener: ln, conns: g.conns}) })
	}
	return func() {
		cancel()
		for _, ln := range lns {
			ln.Close()
		}
		accepting.Wait()
		g.conns.closeAll()
	}
}
