package cubesandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// cubeSandbox is the vendor's response vocabulary. Only the fields this adapter
// consumes are declared; unknown fields are ignored, as the pinned schema allows.
type cubeSandbox struct {
	SandboxID          string            `json:"sandboxID"`
	TemplateID         string            `json:"templateID"`
	State              string            `json:"state"`
	Domain             string            `json:"domain"`
	EnvdAccessToken    string            `json:"envdAccessToken"`
	TrafficAccessToken string            `json:"trafficAccessToken"`
	Metadata           map[string]string `json:"metadata"`
}

// noRedirect rejects a redirect instead of following it. A redirected control or
// data request would silently move the credential to another origin.
func noRedirect(*http.Request, []*http.Request) error {
	return errors.New("CubeSandbox redirects are not allowed")
}

func newControlClient() *http.Client {
	return &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone(), CheckRedirect: noRedirect, Timeout: controlTimeout}
}

// newDataClient reaches the sandbox data plane through CubeProxy. Outside a node
// the sandbox domain does not resolve, so every destination is dialed at the
// configured proxy address while the virtual Host header stays intact.
func newDataClient(config Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = false
	if config.ProxyNodeIP != "" {
		target := proxyTarget(config)
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, target)
		}
	}
	return &http.Client{Transport: transport, CheckRedirect: noRedirect}
}

// proxyTarget resolves the proxy dial address. A port in ProxyNodeIP wins;
// otherwise the scheme default applies, matching the vendor SDK's default.
func proxyTarget(config Config) string {
	host, port, err := splitHostPort(config.ProxyNodeIP)
	if err != nil || host == "" {
		host, port = config.ProxyNodeIP, ""
	}
	if port == "" {
		if config.ProxyScheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

// endpoint joins the configured CubeAPI base with a pinned path.
func (p *Provider) endpoint(path string) string {
	return strings.TrimRight(p.config.APIURL, "/") + path
}

// sandboxDomain is the vendor's per-sandbox data-plane domain. The response wins;
// the configured domain is the fallback.
func (p *Provider) sandboxDomain(observed cubeSandbox) string {
	if observed.Domain != "" {
		return observed.Domain
	}
	return p.config.SandboxDomain
}

// request performs one control-plane call. There is no retry: the caller owns
// reconciliation and replay policy, and a lost Create response must never become
// a second Create.
func (p *Provider) request(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, sandbox.ErrInvalid
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint(path), reader)
	if err != nil {
		return nil, sandbox.ErrInvalid
	}
	// The credential header only. Sending both Bearer and X-API-Key would make
	// the server's auth callback see two mutually exclusive credentials.
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := p.control.Do(req)
	if err != nil {
		return nil, errors.Join(errors.New("CubeSandbox control request failed"), ctx.Err())
	}
	defer response.Body.Close()
	if err := controlStatus(response); err != nil {
		return response.Header, err
	}
	if out != nil {
		raw, err := io.ReadAll(io.LimitReader(response.Body, controlResponseBytes+1))
		if err != nil || len(raw) > controlResponseBytes || json.Unmarshal(raw, out) != nil {
			return nil, errors.New("invalid CubeSandbox control response")
		}
	}
	return response.Header, nil
}

// controlStatus maps a control-plane status onto the shared vocabulary. The
// response body is never included: it may echo credential material.
func controlStatus(response *http.Response) error {
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusNotFound:
		return sandbox.ErrNotFound
	case response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusConflict || response.StatusCode >= 500:
		// 408, 409 and 503 (pausing, another lifecycle operation in flight, a
		// failed internal resume) are real failures, never a success. Cleanup
		// stays pending and the caller retries it.
		if after := retryAfter(response.Header); after != "" {
			return fmt.Errorf("CubeSandbox control request returned HTTP %d (retry-after %s)", response.StatusCode, after)
		}
		return fmt.Errorf("CubeSandbox control request returned HTTP %d", response.StatusCode)
	case response.StatusCode == http.StatusBadRequest:
		return fmt.Errorf("CubeSandbox rejected the request: HTTP %d: %w", response.StatusCode, sandbox.ErrInvalid)
	default:
		return fmt.Errorf("CubeSandbox control request returned HTTP %d", response.StatusCode)
	}
}

// retryAfter returns the vendor's retry hint when it is a plain number of
// seconds. Anything else is dropped rather than echoed.
func retryAfter(header http.Header) string {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return ""
	}
	if _, err := strconv.Atoi(value); err != nil {
		return ""
	}
	return value
}

// Health is the vendor's liveness probe. It exists for operators and for the
// opt-in cluster checks; startup deliberately does not depend on it, so a
// temporarily unreachable cluster cannot stop Core from booting.
func (p *Provider) Health(ctx context.Context) error {
	_, err := p.request(ctx, http.MethodGet, "/health", nil, nil)
	return err
}

// list returns the sandboxes that claim this allocation's ownership metadata.
// The vendor filters server-side; every returned entry is re-verified locally so
// a partial or foreign match can never be adopted.
func (p *Provider) list(ctx context.Context, r sandbox.Reference) ([]cubeSandbox, error) {
	if !validReference(r) {
		return nil, sandbox.ErrInvalid
	}
	metadata := url.Values{}
	for key, value := range p.metadata(r) {
		metadata.Set(key, value)
	}
	query := url.Values{"metadata": {metadata.Encode()}}
	var listed []cubeSandbox
	if _, err := p.request(ctx, http.MethodGet, "/sandboxes?"+query.Encode(), nil, &listed); err != nil {
		return nil, err
	}
	for _, entry := range listed {
		if !p.owns(entry.Metadata, r) {
			return nil, sandbox.ErrOwnership
		}
	}
	return listed, nil
}

func (p *Provider) inspectID(ctx context.Context, id string, r sandbox.Reference) (cubeSandbox, error) {
	var observed cubeSandbox
	_, err := p.request(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, &observed)
	if err == nil && (observed.SandboxID != id || !p.owns(observed.Metadata, r)) {
		err = sandbox.ErrOwnership
	}
	return observed, err
}

// inspect resolves exactly one owned sandbox. Two matches are an ownership
// anomaly, never something to pick from arbitrarily.
func (p *Provider) inspect(ctx context.Context, r sandbox.Reference) (cubeSandbox, error) {
	matches, err := p.list(ctx, r)
	if err != nil {
		return cubeSandbox{}, err
	}
	switch len(matches) {
	case 0:
		return cubeSandbox{}, sandbox.ErrNotFound
	case 1:
		return p.inspectID(ctx, matches[0].SandboxID, r)
	default:
		return cubeSandbox{}, sandbox.ErrOwnership
	}
}

func (p *Provider) create(ctx context.Context, body map[string]any) (cubeSandbox, error) {
	var created cubeSandbox
	_, err := p.request(ctx, http.MethodPost, "/sandboxes", body, &created)
	return created, err
}

func (p *Provider) delete(ctx context.Context, id string) error {
	_, err := p.request(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(id), nil, nil)
	return err
}

// refresh extends the TTL of the original sandbox. POST /sandboxes/{id}/resume is
// the deprecated endpoint and is never used for renewal.
func (p *Provider) refresh(ctx context.Context, id string) error {
	_, err := p.request(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/refreshes", map[string]int{"duration": p.config.LeaseSeconds}, nil)
	return err
}
