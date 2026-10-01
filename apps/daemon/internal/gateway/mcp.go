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

// mcpRelay serves one MCP HTTP binding at its server URL's path and relays
// each request to exactly the server URL, query included. The Harness's URL
// carries no query, since a query may hold a credential, so a request with a
// query or for another path is refused. When the binding has a bearer token or
// HTTP headers, it replaces the Harness's credential headers and same-named
// headers with them, and withholds each injected value from response headers
// and trailers.
type mcpRelay struct {
	upstream  url.URL     // the binding's server URL
	path      string      // the escaped path the Harness requests
	inject    http.Header // canonical names, values as sent; empty when the binding has none
	transport http.RoundTripper
}

// newMCPRelay returns the binding's handler and the path the Harness appends
// to the listener's address. A binding with a bearer token or HTTP headers
// needs an https server URL, so no injected value crosses a network in
// plaintext. Errors name a header but never carry a value.
func newMCPRelay(b agent.MCPBinding, t http.RoundTripper) (*mcpRelay, string, error) {
	u, err := url.Parse(b.ServerURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" {
		return nil, "", errors.New("server URL is not an absolute http or https URL")
	}
	m := &mcpRelay{upstream: *u, path: u.EscapedPath(), inject: http.Header{}}
	if m.path == "" {
		m.path = "/"
	}
	var secrets []string
	if b.BearerToken != nil {
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
	if len(m.inject) > 0 && u.Scheme != "https" {
		return nil, "", errors.New("a bearer token or HTTP headers need an https server URL")
	}
	m.transport = withhold(t, secrets...)
	return m, u.EscapedPath(), nil
}

func (m *mcpRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, _, hasQuery, ok := requestTarget(r)
	switch {
	case !ok:
		http.Error(w, "origin-form request target required", http.StatusBadRequest)
		return
	case hasQuery:
		http.Error(w, "the MCP endpoint takes no query", http.StatusBadRequest)
		return
	case path != m.path:
		http.NotFound(w, r)
		return
	}
	reverseProxy(m.transport, func(pr *httputil.ProxyRequest) {
		upstream := m.upstream
		pr.Out.URL = &upstream
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
