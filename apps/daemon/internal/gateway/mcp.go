package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"

	"golang.org/x/net/http/httpguts"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// mcpRelay serves one MCP HTTP binding: it relays each request to the
// server's origin with the same path and query. When the binding has a bearer
// token or HTTP headers, it replaces the Harness's credential headers and
// same-named headers with them, and withholds each injected value from
// response headers and trailers.
type mcpRelay struct {
	scheme    string
	host      string
	inject    http.Header // canonical names, values as sent; empty when the binding has none
	transport http.RoundTripper
}

// newMCPRelay returns the binding's handler and the path and query the
// Harness appends to the listener's address. Errors name a header but never
// carry a value.
func newMCPRelay(b agent.MCPBinding, t http.RoundTripper) (*mcpRelay, string, error) {
	u, err := url.Parse(b.ServerURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" {
		return nil, "", errors.New("server URL is not an absolute http or https URL")
	}
	m := &mcpRelay{scheme: u.Scheme, host: u.Host, inject: http.Header{}}
	var secrets []string
	if b.BearerToken != nil {
		if u.Scheme != "https" {
			return nil, "", errors.New("a bearer token needs an https server URL")
		}
		token := sentValue(*b.BearerToken)
		m.inject.Set("Authorization", "Bearer "+token)
		secrets = append(secrets, token)
	}
	for name, value := range b.HTTPHeaders {
		key := http.CanonicalHeaderKey(name)
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, "", fmt.Errorf("HTTP header %q is not a valid header", name)
		}
		if m.inject[key] != nil {
			return nil, "", fmt.Errorf("HTTP header %q is repeated or replaces the bearer token", name)
		}
		v := sentValue(value)
		m.inject[key] = []string{v}
		secrets = append(secrets, v)
	}
	m.transport = withhold(t, secrets...)
	suffix := u.EscapedPath()
	if u.RawQuery != "" || u.ForceQuery {
		suffix += "?" + u.RawQuery
	}
	return m, suffix, nil
}

func (m *mcpRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, query, hasQuery, ok := requestTarget(r)
	if !ok {
		http.Error(w, "origin-form request target required", http.StatusBadRequest)
		return
	}
	upstream := &url.URL{Scheme: m.scheme, Host: m.host, Path: r.URL.Path, RawPath: path,
		RawQuery: query, ForceQuery: hasQuery && query == ""}
	reverseProxy(m.transport, func(pr *httputil.ProxyRequest) {
		pr.Out.URL = upstream
		pr.Out.Host = ""
		if len(m.inject) == 0 {
			return
		}
		stripCredentials(pr.Out.Header)
		for name := range pr.Out.Header {
			if m.inject[http.CanonicalHeaderKey(name)] != nil {
				delete(pr.Out.Header, name)
			}
		}
		for name, values := range m.inject {
			pr.Out.Header[name] = slices.Clone(values)
		}
	}).ServeHTTP(w, r)
}
