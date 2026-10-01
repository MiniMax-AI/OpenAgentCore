package gateway

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// modelRelay serves one frozen model upstream. It relays only the routes the
// protocol declares, injects the declared credential, keeps the base URL's
// path and withholds the key from response headers and trailers.
type modelRelay struct {
	protocol   modelprovider.Protocol
	scheme     string
	host       string
	targets    map[string]*url.URL // upstream path of each declared route, by route path
	credential modelprovider.Credential
	key        string
	transport  http.RoundTripper
}

func newModelRelay(p modelprovider.Provider, t http.RoundTripper) (*modelRelay, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	credential, err := modelprovider.UpstreamCredential(p.Protocol)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return nil, err
	}
	m := &modelRelay{protocol: p.Protocol, scheme: base.Scheme, host: base.Host, targets: map[string]*url.URL{},
		credential: credential, key: p.APIKey, transport: withhold(t, p.APIKey)}
	for _, route := range modelprovider.Routes(p.Protocol) {
		raw := modelprovider.UpstreamPath(base.EscapedPath(), route.Path)
		path, err := url.PathUnescape(raw)
		if err != nil {
			return nil, err
		}
		m.targets[route.Path] = &url.URL{Path: path, RawPath: raw}
	}
	return m, nil
}

func (m *modelRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, query, hasQuery, ok := requestTarget(r)
	route, err := modelprovider.LookupRoute(m.protocol, r.Method, path)
	switch {
	case !ok:
		http.NotFound(w, r)
		return
	case errors.Is(err, modelprovider.ErrMethodNotAllowed):
		http.Error(w, "method not declared for this route", http.StatusMethodNotAllowed)
		return
	case err != nil:
		http.NotFound(w, r)
		return
	case wantsUpgrade(r) && !route.WebSocket:
		http.Error(w, "upgrade not declared for this route", http.StatusBadRequest)
		return
	}
	target := m.targets[route.Path]
	upstream := &url.URL{Scheme: m.scheme, Host: m.host, Path: target.Path, RawPath: target.RawPath,
		RawQuery: query, ForceQuery: hasQuery && query == ""}
	reverseProxy(m.transport, func(pr *httputil.ProxyRequest) {
		pr.Out.URL = upstream
		pr.Out.Host = ""
		stripCredentials(pr.Out.Header)
		pr.Out.Header.Set(m.credential.Header, m.credential.Value(m.key))
	}).ServeHTTP(w, r)
}
