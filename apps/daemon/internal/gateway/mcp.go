package gateway

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// mcpRelay serves one MCP HTTP binding: it relays each request to the
// server's origin with the same path and query. When the binding has a bearer
// token, it injects the token and withholds it from response headers and
// trailers.
type mcpRelay struct {
	scheme    string
	host      string
	token     *string
	transport http.RoundTripper
}

// newMCPRelay returns the binding's handler and the path and query the
// Harness appends to the listener's address.
func newMCPRelay(s proto.MCPHTTPServer, t http.RoundTripper) (*mcpRelay, string, error) {
	u, err := url.Parse(s.ServerURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" {
		return nil, "", errors.New("server URL is not an absolute http or https URL")
	}
	m := &mcpRelay{scheme: u.Scheme, host: u.Host, transport: t}
	if s.BearerToken != nil {
		if u.Scheme != "https" {
			return nil, "", errors.New("a bearer token needs an https server URL")
		}
		token := *s.BearerToken
		m.token = &token
		m.transport = withhold(t, token)
	}
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
		if m.token != nil {
			stripCredentials(pr.Out.Header)
			pr.Out.Header.Set("Authorization", "Bearer "+*m.token)
		}
	}).ServeHTTP(w, r)
}
