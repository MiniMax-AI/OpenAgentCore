package cubesandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// readinessPath is served by the Runtime image on ReadinessPort (§8.1). The same
// port is the Cube template probe, so the endpoint is the only readiness
// contract for both the build and the running sandbox.
const readinessPath = "/healthz"

// waitReadyTimeout bounds how long Create waits for provider readiness evidence.
// Core polls GetInfo every five seconds afterwards, so an unready sandbox after
// this window is reported as unready rather than failing the allocation.
const waitReadyTimeout = 2 * time.Minute

// dataRequest builds a data-plane request for one sandbox port. The virtual Host
// header addresses the sandbox through CubeProxy; the dialer targets the proxy
// node, so no DNS resolution of the sandbox domain is required.
func (p *Provider) dataRequest(ctx context.Context, method string, observed cubeSandbox, port int, path string, query url.Values, body io.Reader, contentType string) (*http.Request, error) {
	target := url.URL{
		Scheme: p.config.ProxyScheme,
		Host:   strconv.Itoa(port) + "-" + observed.SandboxID + "." + p.sandboxDomain(observed),
		Path:   path,
	}
	if len(query) != 0 {
		target.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, sandbox.ErrInvalid
	}
	if observed.EnvdAccessToken != "" {
		req.Header.Set("X-Access-Token", observed.EnvdAccessToken)
	}
	if observed.TrafficAccessToken != "" {
		req.Header.Set("e2b-traffic-access-token", observed.TrafficAccessToken)
		req.Header.Set("cube-traffic-access-token", observed.TrafficAccessToken)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// ready probes the in-sandbox readiness endpoint. It returns a machine reason
// for logging that never carries a response body: the endpoint is ours, but a
// proxy or a foreign server could be answering it.
func (p *Provider) ready(ctx context.Context, observed cubeSandbox, r sandbox.Reference) (bool, string) {
	probe, cancel := context.WithTimeout(ctx, readinessProbeTimeout)
	defer cancel()
	req, err := p.dataRequest(probe, http.MethodGet, observed, ReadinessPort, readinessPath, nil, nil, "")
	if err != nil {
		return false, "invalid readiness request"
	}
	response, err := p.data.Do(req)
	if err != nil {
		return false, "readiness endpoint unreachable"
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Sprintf("readiness endpoint returned HTTP %d", response.StatusCode)
	}
	return true, ""
}

const readinessProbeTimeout = 10 * time.Second

// waitReady waits, with a bounded deadline, for the readiness endpoint to answer
// 2xx. A timeout is not a failure of the allocation: Core keeps polling GetInfo
// and settles creation only from real provider evidence.
func (p *Provider) waitReady(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	deadline := time.Now().Add(waitReadyTimeout)
	if existing, ok := ctx.Deadline(); ok && existing.Before(deadline) {
		deadline = existing
	}
	for {
		info, err := p.GetInfo(ctx, r)
		if err != nil || info.BootstrapComplete {
			return info, err
		}
		if !time.Now().Before(deadline) {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return info, nil
		case <-time.After(readinessPollInterval):
		}
	}
}

const readinessPollInterval = 2 * time.Second

// errUnconfirmed wraps an uncertain data-plane outcome so the caller reclaims the
// allocation instead of replaying the operation.
func errUnconfirmed(err error) error {
	return errors.Join(sandbox.ErrCommandUnconfirmed, err)
}
