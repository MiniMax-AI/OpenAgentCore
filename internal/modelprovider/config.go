// Package modelprovider validates the frozen upstream connection supplied to a
// native Harness and declares the native routes and credential header that the
// Session's credential gateway relays for each protocol.
package modelprovider

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
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

// MaxAPIKeyLength is the longest API key accepted, in bytes.
const MaxAPIKeyLength = 16384

// FieldError rejects one Provider field, named as in JSON. Its message never
// contains the submitted value.
type FieldError struct {
	Field   string
	message string
}

func (e *FieldError) Error() string { return e.message }

// Protocols is the single vocabulary of supported upstream protocol formats.
// The Harness catalog generator projects it to the TypeScript client.
func Protocols() []Protocol { return []Protocol{Anthropic, Responses, ChatCompletions} }

func (p Protocol) Valid() bool { return slices.Contains(Protocols(), p) }

// Validate is the only provider rule, for Core and the Runtime alike. The base
// URL is https; loopbackHTTP also admits http to a loopback IP address, which
// only the credential gateway's listener that a view hands its Harness uses.
// The first rejected field is reported in the order base URL, protocol, API
// key, token limits.
func (p Provider) Validate(loopbackHTTP bool) error {
	u, err := url.Parse(p.BaseURL)
	if err != nil || !(u.Scheme == "https" || loopbackHTTP && u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback()) ||
		!validHost(u) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(p.BaseURL, "\x00\r\n") {
		return &FieldError{"base_url", "model provider requires an HTTPS base_url without credentials, query or fragment"}
	}
	// The anthropic routes begin with the version path, so a base path ending
	// in "/v1" or "/v1/" would reach "/v1/v1/messages".
	if p.Protocol == Anthropic && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
		return &FieldError{"base_url", "an anthropic base_url excludes the /v1 version path"}
	}
	if !p.Protocol.Valid() {
		return &FieldError{"protocol", "unsupported model provider protocol"}
	}
	if strings.TrimSpace(p.APIKey) == "" || len(p.APIKey) > MaxAPIKeyLength || strings.ContainsAny(p.APIKey, "\x00\r\n") {
		return &FieldError{"api_key", "invalid model provider API key"}
	}
	if p.ContextWindow < 0 {
		return &FieldError{"context_window", "invalid model token limits"}
	}
	if p.MaxOutputTokens < 0 || p.MaxOutputTokens > p.ContextWindow {
		return &FieldError{"max_output_tokens", "invalid model token limits"}
	}
	return nil
}

// hostProfile converts a domain as URL host parsing does (UTS #46 without
// hyphen or STD3 restrictions), rejecting invalid labels such as bad punycode.
var hostProfile = idna.New(idna.MapForLookup(), idna.BidiRule(), idna.StrictDomainName(false), idna.CheckHyphens(false))

// validHost requires a usable host: an IP address, or a domain whose labels
// are nonempty letters, digits, hyphens and underscores and whose final label
// is not numeric. Any port must be in 1-65535.
func validHost(u *url.URL) bool {
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	if net.ParseIP(u.Hostname()) != nil {
		return true
	}
	ascii, err := hostProfile.ToASCII(u.Hostname())
	if err != nil {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(ascii, "."), ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label == "xn--" || strings.IndexFunc(label, invalidHostRune) >= 0 {
			return false
		}
	}
	// A numeric final label makes the host an IPv4 address, which ParseIP rejected.
	return strings.Trim(labels[len(labels)-1], "0123456789") != ""
}

func invalidHostRune(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
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
