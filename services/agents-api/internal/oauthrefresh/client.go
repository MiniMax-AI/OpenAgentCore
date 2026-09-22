package oauthrefresh

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var ErrRefresh = errors.New("OAuth credential refresh failed")

// Client has immutable network policy. Grant state belongs to PostgreSQL, not a
// process-local TokenSource cache, so replacement and deletion stay authoritative.
type Client struct {
	transport *http.Transport
}

// NewClient permits public HTTPS endpoints by default. Operators may trust exact
// HTTPS origins for private issuers; tenants cannot relax this network policy.
func NewClient(privateOrigins []string) (*Client, error) {
	policy, err := newNetworkPolicy(privateOrigins)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = policy.dialContext
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	return &Client{transport: transport}, nil
}

func (c *Client) Refresh(ctx context.Context, input Request) (Token, error) {
	if c == nil || c.transport == nil {
		return Token{}, ErrRefresh
	}
	endpoint, err := parseEndpoint(input.TokenEndpoint)
	if err != nil {
		return Token{}, ErrRefresh
	}
	style := oauth2.AuthStyleInParams
	switch input.AuthMethod {
	case "none":
		if input.ClientSecret != "" {
			return Token{}, ErrRefresh
		}
	case "client_secret_post":
	case "client_secret_basic":
		style = oauth2.AuthStyleInHeader
	default:
		return Token{}, ErrRefresh
	}
	client := &http.Client{
		Transport:     refreshFormTransport{base: c.transport, endpoint: endpoint.String(), input: input},
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRefresh },
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	config := oauth2.Config{ClientID: input.ClientID, ClientSecret: input.ClientSecret,
		Endpoint: oauth2.Endpoint{TokenURL: endpoint.String(), AuthStyle: style}}
	token, err := config.TokenSource(ctx, &oauth2.Token{RefreshToken: input.RefreshToken}).Token()
	// RetrieveError contains provider response bodies and must never escape.
	if err != nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return Token{}, ErrRefresh
	}
	result := Token{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken}
	if !token.Expiry.IsZero() {
		if !token.Expiry.After(time.Now()) {
			return Token{}, ErrRefresh
		}
		expiry := token.Expiry.UTC()
		result.ExpiresAt = &expiry
	}
	return result, nil
}

// oauth2's refresh TokenSource omits scope/resource. Add only these pinned grant
// fields to its one token request, retaining the library's auth and response logic.
type refreshFormTransport struct {
	base     http.RoundTripper
	endpoint string
	input    Request
}

func (t refreshFormTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodPost || request.URL.String() != t.endpoint {
		return nil, ErrRefresh
	}
	body, err := io.ReadAll(request.Body)
	request.Body.Close()
	if err != nil {
		return nil, ErrRefresh
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, ErrRefresh
	}
	// Explicit client authentication also preserves empty strings from the stored
	// grant; the library omits them when using AuthStyleInParams.
	if t.input.AuthMethod != "client_secret_basic" {
		values.Set("client_id", t.input.ClientID)
		if t.input.AuthMethod == "client_secret_post" {
			values.Set("client_secret", t.input.ClientSecret)
		}
	}
	if t.input.Scope != nil {
		values.Set("scope", *t.input.Scope)
	}
	if t.input.Resource != nil {
		values.Set("resource", *t.input.Resource)
	}
	clone := request.Clone(request.Context())
	encoded := values.Encode()
	clone.Body = io.NopCloser(strings.NewReader(encoded))
	clone.ContentLength = int64(len(encoded))
	clone.GetBody = nil
	return t.base.RoundTrip(clone)
}
