package opensandbox

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	osdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// privatePrefixes are the addresses a public-internet sandbox must not reach.
// Loopback is not among them: inside the sandbox's network namespace it is the
// sandbox itself (execd and the sidecar's DNS proxy), never the host.
var privatePrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"),
}

type resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// networkPolicy maps Egress to the OpenSandbox egress policy. The sidecar lets a
// deny rule win over any allow, so a public sandbox, which denies the private
// ranges, needs Core at global addresses; any other sandbox denies by default
// and allows Core, the model provider endpoints and the named domains.
func networkPolicy(ctx context.Context, b sandbox.Bootstrap, resolve resolver) (*osdk.NetworkPolicy, error) {
	core, err := url.Parse(b.CoreURL)
	if err != nil || core.Hostname() == "" {
		return nil, sandbox.ErrInvalid
	}
	coreHost, corePort := strings.ToLower(core.Hostname()), 443
	if core.Scheme == "http" {
		corePort = 80
	}
	if port := core.Port(); port != "" {
		if corePort, err = strconv.Atoi(port); err != nil {
			return nil, sandbox.ErrInvalid
		}
	}
	var rules []osdk.NetworkRule
	if b.Egress.Public {
		addrs, err := hostAddrs(ctx, coreHost, resolve)
		if err != nil {
			return nil, err
		}
		// Opening a private Core address would open all of its ports.
		if slices.ContainsFunc(addrs, func(a netip.Addr) bool { return !isGlobal(a) }) {
			return nil, fmt.Errorf("%w: a public OpenSandbox sandbox reaches Core only at global addresses", sandbox.ErrInvalid)
		}
		for _, prefix := range privatePrefixes {
			rules = append(rules, osdk.NetworkRule{Action: "deny", Target: prefix.String()})
		}
		return &osdk.NetworkPolicy{DefaultAction: "allow", Egress: rules}, nil
	}
	// allow adds a domain rule, or an IP rule on the TCP port. Only Core's own
	// address may be non-global.
	allow := func(host string, port int, isCore bool) {
		rule := osdk.NetworkRule{Action: "allow", Target: host}
		if addr, err := netip.ParseAddr(host); err == nil {
			if !isCore && !isGlobal(addr) {
				return
			}
			rule.Ports = []int{port}
		}
		if !slices.ContainsFunc(rules, func(r osdk.NetworkRule) bool { return r.Target == rule.Target && slices.Equal(r.Ports, rule.Ports) }) {
			rules = append(rules, rule)
		}
	}
	allow(coreHost, corePort, true)
	for _, endpoint := range b.Egress.Endpoints {
		host, port, err := sandbox.SplitEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		allow(strings.ToLower(host), port, false)
	}
	for _, domain := range b.Egress.Domains {
		allow(strings.ToLower(domain), 0, false)
	}
	return &osdk.NetworkPolicy{DefaultAction: "deny", Egress: rules}, nil
}

func hostAddrs(ctx context.Context, host string, resolve resolver) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	addrs, err := resolve(ctx, host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("%w: the Core host cannot be resolved", sandbox.ErrInvalid)
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	return addrs, nil
}

func isGlobal(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !slices.ContainsFunc(privatePrefixes, func(p netip.Prefix) bool { return p.Contains(addr) })
}
