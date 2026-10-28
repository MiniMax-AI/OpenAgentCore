package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// allocationEgress derives what the allocation may reach from its immutable
// inputs only: the deployment generation's network cap, the Session's frozen
// Environment network policy and the model provider the Session froze at
// creation. Create and Resume send the same value. A policy that the cap no
// longer covers fails the attempt with the admission error.
func (r *runtimeLifecycle) allocationEgress(ctx context.Context, owner deployment.Allocation, network sandbox.Network) (sandbox.Egress, error) {
	session, err := r.sessions.GetSession(ctx, owner.TenantID, owner.SessionID)
	if err != nil {
		return sandbox.Egress{}, err
	}
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil {
		return sandbox.Egress{}, sandbox.ErrInvalid
	}
	provider, err := r.sessions.SessionModelExecution(ctx, owner.TenantID, owner.SessionID)
	baseURL := ""
	switch {
	case err == nil:
		baseURL = provider.BaseURL
	case !errors.Is(err, sessions.ErrNotFound):
		return sandbox.Egress{}, err
	}
	return egressFor(network, snapshot.Environment.Network, baseURL)
}

// admitNetwork checks an Environment network policy at Session creation:
// disabled and restricted need a Provider that can restrict, and every
// restricted domain must lie within the deployment cap.
func admitNetwork(support sandbox.NetworkSupport, network sandbox.Network, policy *v1.EnvironmentNetworkInput) error {
	if policy != nil && policy.Access != "enabled" && !support.Restrict {
		return &sandbox.ConfigurationError{Class: sandbox.ConfigurationInvalid, Code: "sandbox_network_unsupported", Param: "environment.network.access",
			Message: "The sandbox provider cannot enforce disabled or restricted networking."}
	}
	_, err := egressFor(network, policy, "")
	return err
}

// egressFor applies the cap and the Environment policy. The Harness runs inside
// the sandbox, so the port of the Session's model provider is always reachable
// on public addresses; a loopback host is the sandbox itself and needs no
// entry. A model host the cap covers, by name or "*.suffix", also joins
// PrivateDomains as its exact name, so a private model host keeps working
// under every policy. A nil policy equals enabled.
func egressFor(network sandbox.Network, policy *v1.EnvironmentNetworkInput, modelBaseURL string) (sandbox.Egress, error) {
	egress := sandbox.Egress{Public: network.Public, PrivateDomains: append([]string{}, network.Domains...), Domains: []string{}, Endpoints: []string{}}
	if policy != nil && policy.Access != "enabled" {
		egress.Public, egress.PrivateDomains = false, []string{}
		for i, domain := range policy.AllowedDomains {
			name := strings.ToLower(domain)
			switch {
			case inCap(network.Domains, name):
				egress.PrivateDomains = append(egress.PrivateDomains, name)
			case network.Public:
				egress.Domains = append(egress.Domains, name)
			default:
				return sandbox.Egress{}, &sandbox.ConfigurationError{Class: sandbox.ConfigurationInvalid, Code: "sandbox_network_outside_cap", Param: fmt.Sprintf("environment.network.allowed_domains[%d]", i),
					Message: fmt.Sprintf("%s is outside the network this deployment allows.", domain)}
			}
		}
	}
	if u, err := url.Parse(modelBaseURL); err == nil {
		if host := strings.ToLower(u.Hostname()); host != "" && !loopbackHost(host) {
			port := u.Port()
			if port == "" {
				port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
			}
			endpoint := net.JoinHostPort(host, port)
			if _, _, err := sandbox.SplitEndpoint(endpoint); err == nil {
				egress.Endpoints = append(egress.Endpoints, endpoint)
			}
			if inCap(network.Domains, host) {
				egress.PrivateDomains = append(egress.PrivateDomains, host)
			}
		}
	}
	slices.Sort(egress.PrivateDomains)
	egress.PrivateDomains = slices.Compact(egress.PrivateDomains)
	slices.Sort(egress.Domains)
	egress.Domains = slices.Compact(egress.Domains)
	return egress, nil
}

// inCap reports whether the cap lists name exactly or as a subdomain through a
// "*.suffix" entry; the suffix itself is not covered.
func inCap(domains []string, name string) bool {
	return slices.ContainsFunc(domains, func(d string) bool {
		suffix, wild := strings.CutPrefix(d, "*.")
		return d == name || wild && strings.HasSuffix(name, "."+suffix)
	})
}

func loopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
