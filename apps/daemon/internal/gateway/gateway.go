// Package gateway is the Session gateway on the agent host. Inside the
// Session's loopback-only network namespace it serves one listener per frozen
// model upstream, one per MCP HTTP binding and, when the view has one, a
// generic proxy, so the Harness never holds an upstream credential and has no
// network route of its own.
//
// A listener's identity selects its upstream and credential; nothing is routed
// by hostname. A model listener relays the declared native routes of its
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
// pass unchanged. The end of the Session closes every connection, tunnels and
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
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
)

// Config is what one Session's gateway serves. It holds credentials: keep it
// in memory and never log it.
type Config struct {
	// Models are the frozen model upstreams, each under the adapter's name for
	// it. Names are unique.
	Models []Model
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

// Model is one frozen model upstream.
type Model struct {
	Name     string
	Provider modelprovider.Provider
}

// Endpoints is what the Harness is given in place of upstreams and
// credentials. Every URL is plain HTTP on the Session's loopback.
type Endpoints struct {
	// Placeholder is the credential a Harness sends to a model listener. It
	// is not secret; the listener removes it.
	Placeholder string
	// Models maps each model upstream's name to its listener's base URL,
	// http://127.0.0.1:<port>, with no path. The listener adds the frozen
	// base URL's path.
	Models map[string]string
	// MCP maps each binding's server label to the URL the Harness uses: its
	// listener with the server URL's path and no query.
	MCP map[string]string
	// Proxy is the generic proxy's URL, for HTTP and HTTPS proxy settings,
	// or empty when Config.Proxy is unset.
	Proxy string
}

// SessionNetwork is the Session's network namespace: the file sessionview's
// network hook receives. Start uses it only while it runs.
type SessionNetwork struct {
	Namespace *os.File
}

// ProxyPort is the generic proxy's port in the Session's namespace. The model
// listeners take the following ports in Config order, then the MCP listeners,
// whether or not the proxy is served.
// The namespace is the Session's own and the gateway listens before the
// Harness starts, so the ports are free; fixing them lets the Harness's
// environment be built before the namespace exists.
const ProxyPort = 17100

// maxListeners bounds the listeners of one Session.
const maxListeners = 256

var (
	// ErrInvalidConfig is a Config that Plan and Start reject. The message
	// names the item by its name or label and never includes a credential.
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

// Start validates cfg, opens its listeners inside the Session's network
// namespace and serves them from the daemon until ctx ends. It returns the
// same Endpoints as Plan(cfg). It is meant to run in sessionview's network
// hook; nothing listens outside the namespace. The end of ctx closes the
// listeners and every connection.
func Start(ctx context.Context, n SessionNetwork, cfg Config) (Endpoints, error) {
	g, err := build(ctx, cfg)
	if err != nil {
		return Endpoints{}, err
	}
	if n.Namespace == nil {
		return Endpoints{}, fmt.Errorf("%w: no namespace", ErrNetwork)
	}
	ports := g.fixedPorts()
	lns, err := listen(n.Namespace, ports)
	if err != nil {
		return Endpoints{}, err
	}
	g.serve(ctx, lns)
	return g.endpoints(ports), nil
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
	name    string // model name or MCP server label
	suffix  string // MCP: the server URL's path
	handler http.Handler
}

type gateway struct {
	listeners  []listener // the proxy when served, then models, then MCP, in Config order
	transports []*http.Transport
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidConfig}, args...)...)
}

// build validates cfg and makes each listener's handler. Every upstream dial
// ends with session.
func build(session context.Context, cfg Config) (*gateway, error) {
	if n := 1 + len(cfg.Models) + len(cfg.MCP); n > maxListeners {
		return nil, invalid("%d listeners, at most %d", n, maxListeners)
	}
	host := relayTransport(cfg.RootCAs, sessionDial(session, (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext))
	g := &gateway{transports: []*http.Transport{host}}
	// The proxy forwards on its own transport; environment-origin MCP relays
	// use a relay transport.
	var forward, sandbox *http.Transport
	if cfg.OpenNetwork != nil {
		dial := sessionDial(session, sandboxDialer(cfg.OpenNetwork))
		forward, sandbox = newTransport(cfg.RootCAs, dial), relayTransport(cfg.RootCAs, dial)
		g.transports = append(g.transports, forward, sandbox)
	}
	if cfg.Proxy {
		g.listeners = append(g.listeners, listener{role: roleProxy, handler: newProxy(cfg.OpenNetwork, forward)})
	}

	names := map[string]bool{}
	for _, m := range cfg.Models {
		if m.Name == "" || names[m.Name] {
			return nil, invalid("model upstream name %q is empty or repeated", m.Name)
		}
		names[m.Name] = true
		h, err := newModelRelay(m.Provider, host)
		if err != nil {
			return nil, invalid("model upstream %q: %v", m.Name, err)
		}
		g.listeners = append(g.listeners, listener{role: roleModel, name: m.Name, handler: h})
	}

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
		g.listeners = append(g.listeners, listener{role: roleMCP, name: b.ServerLabel, suffix: suffix, handler: h})
	}
	return g, nil
}

func (g *gateway) endpoints(ports []int) Endpoints {
	e := Endpoints{Placeholder: modelprovider.Placeholder, Models: map[string]string{}, MCP: map[string]string{}}
	for i, l := range g.listeners {
		base := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[i]))
		switch l.role {
		case roleProxy:
			e.Proxy = base
		case roleModel:
			e.Models[l.name] = base
		case roleMCP:
			e.MCP[l.name] = base + l.suffix
		}
	}
	return e
}

// quiet discards what net/http would log: a logged request or upstream error
// could carry more than the gateway chooses to reveal.
var quiet = log.New(io.Discard, "", 0)

// serve serves each listener with its handler until ctx ends. The end of ctx
// cancels every request, which closes its upstream side, and aborts every
// connection the listeners accepted, which ends a relay blocked on a Harness
// that does not read.
func (g *gateway) serve(ctx context.Context, lns []*net.TCPListener) {
	conns := &sessionConns{open: map[*sessionConn]struct{}{}}
	for i, ln := range lns {
		srv := &http.Server{
			Handler:           g.listeners[i].handler,
			ReadHeaderTimeout: 30 * time.Second,
			ErrorLog:          quiet,
			BaseContext:       func(net.Listener) context.Context { return ctx },
		}
		go srv.Serve(sessionListener{TCPListener: ln, conns: conns})
		context.AfterFunc(ctx, func() { srv.Close() })
	}
	context.AfterFunc(ctx, func() {
		conns.abort()
		for _, t := range g.transports {
			t.CloseIdleConnections()
		}
	})
}
