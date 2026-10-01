package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
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
)

// newTransport returns an upstream transport that relays requests as they
// are: no proxy from the environment, no added compression and no redirects,
// which http.Transport never follows.
func newTransport(tlsConfig *tls.Config, dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	if tlsConfig != nil {
		tlsConfig = tlsConfig.Clone()
	}
	return &http.Transport{
		DialContext:           dial,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
	}
}

// sandboxDialer connects through a new Network stream for each connection, so
// the sandbox resolves the name and the connection has sandbox origin.
func sandboxDialer(open func(context.Context) (sandboxlink.Stream, error)) func(context.Context, string, string) (net.Conn, error) {
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

func connectSandbox(ctx context.Context, open func(context.Context) (sandboxlink.Stream, error), host string, port uint16) (*sandboxnet.Conn, error) {
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
	return &httputil.ReverseProxy{Rewrite: rewrite, Transport: t, FlushInterval: -1, ErrorLog: quiet, ErrorHandler: relayFailed}
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
