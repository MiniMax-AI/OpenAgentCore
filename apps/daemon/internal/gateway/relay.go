package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

const (
	// dialTimeout bounds a connection from the agent host.
	dialTimeout = 30 * time.Second
	// connectTimeout bounds the sandbox's resolution and dial for one
	// Connect.
	connectTimeout = 30 * time.Second
	// connectMargin is how long the gateway waits beyond connectTimeout for
	// the stream to open and the sandbox's answer to arrive.
	connectMargin = 5 * time.Second
)

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// newTransport returns an upstream transport that relays requests as they
// are: no proxy from the environment, no added compression and no redirects,
// which http.Transport never follows. TLS trusts roots, or the system roots
// when roots is nil, and always verifies the destination's hostname.
func newTransport(roots *x509.CertPool, dial dialFunc) *http.Transport {
	return &http.Transport{
		DialContext:           dial,
		TLSClientConfig:       &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
	}
}

// relayTransport returns a transport for model and MCP relays, which inject
// credentials. It closes each HTTP/1 connection after its response, so none
// is ever idle: http.Transport logs the bytes an idle HTTP/1 connection
// receives, and those can echo the credential. A negative MaxIdleConnsPerHost
// keeps HTTP/1 connections out of the idle pool; HTTP/2 connections stay
// shared in their own pool, and the HTTP/2 transport logs no received bytes.
func relayTransport(roots *x509.CertPool, dial dialFunc) *http.Transport {
	t := newTransport(roots, dial)
	t.MaxIdleConnsPerHost = -1
	return t
}

// sessionDial binds each dial to the Session as well as to its own context.
// http.Transport detaches a dial from the request that started it, and
// CloseIdleConnections cancels only dials that no request waits for.
func sessionDial(session context.Context, dial dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(session, cancel)
		defer stop()
		return dial(ctx, network, addr)
	}
}

// sandboxDialer connects through a new Network stream for each connection, so
// the sandbox resolves the name and the connection has sandbox origin.
func sandboxDialer(open func(context.Context) (sandboxlink.Stream, error)) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, &sandboxnet.Error{Code: sandboxnet.CodeUnsupportedNetwork, Effect: sandboxwire.EffectNone}
		}
		host, port, err := splitHostPort(addr)
		if err != nil {
			return nil, err
		}
		return connectSandbox(ctx, open, host, port)
	}
}

// connectSandbox opens a Network stream and connects through it. A local
// deadline bounds both, so a sandbox that never answers cannot hold the
// connection open; the returned Conn outlives it.
func connectSandbox(ctx context.Context, open func(context.Context) (sandboxlink.Stream, error), host string, port uint16) (*sandboxnet.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout+connectMargin)
	defer cancel()
	s, err := open(ctx)
	if err != nil {
		return nil, err
	}
	return sandboxnet.Connect(ctx, s, host, port, connectTimeout)
}

// splitHostPort splits an authority into an unbracketed host and a nonzero
// port.
func splitHostPort(addr string) (string, uint16, error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, &sandboxnet.Error{Code: sandboxnet.CodeInvalidArgument, Effect: sandboxwire.EffectNone, Cause: err}
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil || port == 0 || host == "" {
		return "", 0, &sandboxnet.Error{Code: sandboxnet.CodeInvalidArgument, Effect: sandboxwire.EffectNone}
	}
	return host, uint16(port), nil
}

// reverseProxy relays one request through t after rewrite. It flushes every
// write, so event streams pass as they arrive, and relays protocol upgrades.
func reverseProxy(t http.RoundTripper, rewrite func(*httputil.ProxyRequest)) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{Rewrite: rewrite, Transport: t, ModifyResponse: checkUpgrade, FlushInterval: -1, ErrorLog: quiet, ErrorHandler: relayFailed}
}

var errUpgrade = errors.New("the upstream switched to a protocol the request did not ask for")

// checkUpgrade accepts a 101 response only when the request asked for an
// upgrade, the response switches to that protocol and its body is the
// connection. ReverseProxy closes the body of a response checkUpgrade
// rejects, but not after its own upgrade errors. A 101 hands the connection
// to the relay, so the connection also closes when the request ends, which
// the end of the Session ends too.
func checkUpgrade(res *http.Response) error {
	if res.StatusCode != http.StatusSwitchingProtocols {
		return nil
	}
	body := res.Body
	context.AfterFunc(res.Request.Context(), func() { body.Close() })
	want, got := upgradeType(res.Request.Header), upgradeType(res.Header)
	if _, ok := body.(io.ReadWriteCloser); !ok || want == "" || !printable(want) || !printable(got) || !strings.EqualFold(want, got) {
		return errUpgrade
	}
	return nil
}

// upgradeType returns the protocol h upgrades to, as httputil.ReverseProxy
// reads it.
func upgradeType(h http.Header) string {
	if !httpguts.HeaderValuesContainsToken(h["Connection"], "Upgrade") {
		return ""
	}
	return h.Get("Upgrade")
}

// printable reports whether s is printable ASCII.
func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// relayFailed answers a request that reached no upstream answer. The body
// says nothing about the upstream.
func relayFailed(w http.ResponseWriter, _ *http.Request, err error) {
	status := statusOf(err)
	http.Error(w, http.StatusText(status), status)
}

// statusOf maps a failure to reach a destination to an HTTP status.
func statusOf(err error) int {
	var e *sandboxnet.Error
	if errors.As(err, &e) {
		switch e.Code {
		case sandboxnet.CodeInvalidArgument:
			return http.StatusBadRequest
		case sandboxnet.CodeDenied:
			return http.StatusForbidden
		case sandboxnet.CodeTimedOut:
			return http.StatusGatewayTimeout
		}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

// sentValue returns v as the HTTP/1 header writer sends it: each CR and LF
// becomes a space, and surrounding whitespace goes. A relay injects a
// credential in this form and withholds it in this form, so the filter
// matches what the upstream received.
func sentValue(v string) string {
	return textproto.TrimString(strings.NewReplacer("\n", " ", "\r", " ").Replace(v))
}

// withhold returns t, or, when secret is set, a transport that keeps secret
// out of the response headers the Harness receives. secret is the value as
// sent.
func withhold(t http.RoundTripper, secret string) http.RoundTripper {
	if secret == "" {
		return t
	}
	return withholding{next: t, secret: secret}
}

// withholding removes every header and trailer value that contains secret
// from each response, informational ones included, so an upstream that echoes
// the injected credential in a header does not disclose it. Bodies pass
// unchanged.
type withholding struct {
	next   http.RoundTripper
	secret string
}

func (t withholding) RoundTrip(r *http.Request) (*http.Response, error) {
	// The newest trace's hooks run first, so each informational response is
	// cleaned before the reverse proxy relays it.
	r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{
		Got1xxResponse: func(_ int, h textproto.MIMEHeader) error {
			t.remove(http.Header(h))
			return nil
		},
	}))
	resp, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	t.remove(resp.Header)
	// An upgraded response's body is the connection itself, without
	// trailers, and must stay one.
	if resp.StatusCode != http.StatusSwitchingProtocols {
		resp.Body = &withheldTrailers{ReadCloser: resp.Body, resp: resp, t: t}
	}
	return resp, nil
}

// remove deletes each value that contains the secret. A name whose values
// are all removed stays with none, so an announced trailer stays announced.
func (t withholding) remove(h http.Header) {
	for name, values := range h {
		h[name] = slices.DeleteFunc(values, func(v string) bool { return strings.Contains(v, t.secret) })
	}
}

// withheldTrailers cleans the response's trailers when the body ends: they
// have arrived by then, and the reverse proxy relays them only afterwards.
type withheldTrailers struct {
	io.ReadCloser
	resp *http.Response
	t    withholding
}

func (b *withheldTrailers) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.t.remove(b.resp.Trailer)
	}
	return n, err
}

// stripCredentials removes every value of each header in
// modelprovider.StrippedHeaders, matching names case-insensitively.
func stripCredentials(h http.Header) {
	for name := range h {
		for _, s := range modelprovider.StrippedHeaders {
			if strings.EqualFold(name, s) {
				delete(h, name)
				break
			}
		}
	}
}

// wantsUpgrade reports whether r asks for a protocol upgrade, as
// httputil.ReverseProxy recognizes one.
func wantsUpgrade(r *http.Request) bool {
	return httpguts.HeaderValuesContainsToken(r.Header["Connection"], "upgrade")
}

// requestTarget splits an origin-form request target into its escaped path
// and its query exactly as the Harness sent them. ok is false for any other
// form.
func requestTarget(r *http.Request) (path, query string, hasQuery, ok bool) {
	path, query, hasQuery = strings.Cut(r.RequestURI, "?")
	return path, query, hasQuery, strings.HasPrefix(path, "/")
}
