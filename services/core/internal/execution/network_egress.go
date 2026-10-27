package execution

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// allocationEgress derives what the allocation may reach from its immutable
// inputs only: the deployment generation's network cap and the model provider
// the Session froze at creation. Create and Resume send the same value.
func (r *runtimeLifecycle) allocationEgress(ctx context.Context, owner deployment.Allocation, network sandbox.Network) (sandbox.Egress, error) {
	provider, err := r.sessions.SessionModelExecution(ctx, owner.TenantID, owner.SessionID)
	if errors.Is(err, sessions.ErrNotFound) {
		return egressFor(network, ""), nil
	}
	if err != nil {
		return sandbox.Egress{}, err
	}
	return egressFor(network, provider.BaseURL), nil
}

// egressFor applies the cap. The Harness runs inside the sandbox, so the
// host of the Session's model provider is always reachable; a loopback host is
// the sandbox itself and needs no entry.
func egressFor(network sandbox.Network, modelBaseURL string) sandbox.Egress {
	egress := sandbox.Egress{Public: network.Public, PrivateDomains: append([]string{}, network.Domains...), Endpoints: []string{}}
	if u, err := url.Parse(modelBaseURL); err == nil {
		if host := strings.ToLower(u.Hostname()); host != "" && !loopbackHost(host) {
			egress.Endpoints = append(egress.Endpoints, host)
		}
	}
	return egress
}

func loopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
