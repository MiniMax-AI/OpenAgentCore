// Package modelprovider validates the frozen upstream connection supplied to a
// native Harness and declares the native routes and credential header that the
// Session's credential gateway relays for each protocol.
package modelprovider

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
)

type Protocol string

const (
	Anthropic       Protocol = "anthropic"
	Responses       Protocol = "responses"
	ChatCompletions Protocol = "chat_completions"
)

// Provider is the confidential, frozen upstream bundle supplied by Core.
// Engine selection and model identity remain separate execution inputs.
type Provider struct {
	Protocol        Protocol `json:"protocol"`
	BaseURL         string   `json:"base_url"`
	APIKey          string   `json:"api_key"`
	ContextWindow   int32    `json:"context_window,omitempty"`
	MaxOutputTokens int32    `json:"max_output_tokens,omitempty"`
}

var ErrConfiguration = errors.New("invalid model provider configuration")

// Protocols is the single vocabulary of supported upstream protocol formats.
// The Harness catalog generator projects it to the TypeScript client.
func Protocols() []Protocol { return []Protocol{Anthropic, Responses, ChatCompletions} }

func (p Protocol) Valid() bool { return slices.Contains(Protocols(), p) }

// ValidBasePath reports whether a base URL's path suits the protocol. The
// anthropic routes begin with the version path, so an anthropic base URL
// excludes it: a path ending in "/v1", or "/v1/", would reach "/v1/v1/messages".
func (p Protocol) ValidBasePath(path string) bool {
	return p != Anthropic || !strings.HasSuffix(strings.TrimRight(path, "/"), "/v1")
}

func (p Provider) Validate() error {
	if !p.Protocol.Valid() {
		return ErrConfiguration
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(p.BaseURL, "\x00\r\n") || !p.Protocol.ValidBasePath(u.Path) {
		return ErrConfiguration
	}
	// Providers require HTTPS. Loopback HTTP exists only for the gateway
	// listener a view hands its Harness; Core admits only HTTPS providers.
	if u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback()) {
		return ErrConfiguration
	}
	if strings.TrimSpace(p.APIKey) == "" || len(p.APIKey) > 16384 || strings.ContainsAny(p.APIKey, "\x00\r\n") {
		return ErrConfiguration
	}
	if p.ContextWindow < 0 || p.MaxOutputTokens < 0 || p.MaxOutputTokens > p.ContextWindow {
		return ErrConfiguration
	}
	return nil
}

// Placeholder is the credential a Harness receives instead of the upstream key.
// It is not secret and authorizes nothing outside the Session's gateway listener.
const Placeholder = "oac-gateway-placeholder"

// Route is one native HTTP route of a protocol. Path is relative to the
// upstream base URL; UpstreamPath joins the two, and the gateway relays the
// request's query unchanged. No route admits a protocol upgrade.
type Route struct {
	Method string
	Path   string
}

// UpstreamPath is the escaped upstream path for a route: the base URL's escaped
// path with every trailing "/" removed, followed by the route's Path.
func UpstreamPath(baseEscapedPath, routePath string) string {
	return strings.TrimRight(baseEscapedPath, "/") + routePath
}

// Credential is the upstream credential header the gateway injects. Its value
// is Prefix followed by the key.
type Credential struct {
	Header string
	Prefix string
}

func (c Credential) Value(key string) string { return c.Prefix + key }

// StrippedHeaders lists, in canonical MIME form, every inbound credential
// header that a pinned Harness or its SDK can send. The gateway matches the
// names case-insensitively and removes every value of each before it injects
// the upstream credential.
var StrippedHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"X-Api-Key",
	"Api-Key",
	"X-Openai-Actor-Authorization",
	"Cf-Aig-Authorization",
	"X-Amz-Security-Token",
}

// surface is the declared native API of one protocol: the routes the pinned
// Harnesses call and the credential form they send upstream.
type surface struct {
	routes     []Route
	credential Credential
}

var surfaces = map[Protocol]surface{
	Anthropic: {
		routes: []Route{
			{Method: "POST", Path: "/v1/messages"},
			{Method: "POST", Path: "/v1/messages/count_tokens"},
		},
		credential: Credential{Header: "X-Api-Key"},
	},
	Responses: {
		routes:     []Route{{Method: "POST", Path: "/responses"}},
		credential: Credential{Header: "Authorization", Prefix: "Bearer "},
	},
	ChatCompletions: {
		routes:     []Route{{Method: "POST", Path: "/chat/completions"}},
		credential: Credential{Header: "Authorization", Prefix: "Bearer "},
	},
}

var (
	ErrRouteNotFound    = errors.New("model route is not declared")
	ErrMethodNotAllowed = errors.New("model route does not allow this method")
)

// Routes returns the declared routes of a protocol.
func Routes(p Protocol) []Route { return slices.Clone(surfaces[p].routes) }

// UpstreamCredential returns the credential header the gateway injects for p.
func UpstreamCredential(p Protocol) (Credential, error) {
	s, ok := surfaces[p]
	if !ok {
		return Credential{}, ErrConfiguration
	}
	return s.credential, nil
}

// LookupRoute matches a request against the declared routes of p. The path is
// the request's escaped path relative to the base URL and matches exactly,
// with no normalization; the method matches exactly. A declared path with
// another method is ErrMethodNotAllowed; any other path is ErrRouteNotFound.
func LookupRoute(p Protocol, method, path string) (Route, error) {
	s, ok := surfaces[p]
	if !ok {
		return Route{}, ErrConfiguration
	}
	err := ErrRouteNotFound
	for _, route := range s.routes {
		if route.Path != path {
			continue
		}
		if route.Method == method {
			return route, nil
		}
		err = ErrMethodNotAllowed
	}
	return Route{}, err
}
