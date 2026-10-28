package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

func isPrivate(a netip.Addr) bool { return a.IsPrivate() || carrierGradeNAT.Contains(a) }

// resolveTimeout bounds the lookup of Core's address.
const resolveTimeout = 5 * time.Second

// egressNetwork builds one allocation's network policy, rules and defaults
// only, the part that Create and Resume share (restore accepts nothing else);
// both turn strict hostname enforcement and DNS rebind protection off through
// their own options. Rules are first match with default deny:
//  1. host DNS
//  2. deny loopback, link-local, metadata and the rest of the host, and DNS
//     to any other server, so only the host's answers bind names to addresses
//  3. each address of the Core host, on its port
//  4. a port-less domain allow for each exact PrivateDomains name, private
//     addresses included
//  5. public addresses, when Public
//  6. for each "*.apex" PrivateDomains entry, deny the apex unless this Egress
//     names it, then allow the suffix ".apex". A suffix rule matches the apex
//     too but "*.apex" does not, and TLS flows are matched by SNI, so the deny
//     is exact for HTTPS. An Endpoint at the apex skips the deny, since it
//     would come before the Endpoint's allow; the suffix allow then admits
//     that model host on every port
//  7. deny every other private address
//  8. unless Public, a tcp allow on its port for each Endpoint (domain, or a
//     public IP literal) and a port-less domain allow for each Domains name
//
// A plain-text connection to a subdomain that shares an address with a
// resolved apex is refused, since only the address is known there.
func egressNetwork(ctx context.Context, coreURL string, egress sandbox.Egress, report io.Writer) (*sdk.NetworkConfig, error) {
	core, err := url.Parse(coreURL)
	if err != nil || core.Hostname() == "" {
		return nil, sandbox.ErrInvalid
	}
	port := core.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[core.Scheme]
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, sandbox.ErrInvalid
	}
	coreAddresses, err := addresses(ctx, core.Hostname())
	if err != nil {
		return nil, err
	}

	n := &sdk.NetworkConfig{DefaultEgress: sdk.PolicyActionDeny, DefaultIngress: sdk.PolicyActionDeny}
	rule := func(action sdk.PolicyAction, destination, protocol, port string) {
		n.Rules = append(n.Rules, sdk.PolicyRule{Action: action, Direction: sdk.PolicyDirectionEgress, Destination: destination, Protocol: sdk.PolicyProtocol(protocol), Port: port})
	}
	allow := func(destination, protocol, port string) { rule(sdk.PolicyActionAllow, destination, protocol, port) }
	deny := func(destination string) { rule(sdk.PolicyActionDeny, destination, "", "") }
	allow("host", "udp", "53")
	allow("host", "tcp", "53")
	for _, group := range []string{"loopback", "link-local", "metadata", "host"} {
		deny(group)
	}
	rule(sdk.PolicyActionDeny, "*", "udp", "53")
	rule(sdk.PolicyActionDeny, "*", "tcp", "53")
	for _, a := range coreAddresses {
		allow(a.String(), "tcp", port)
	}
	named := slices.Clone(egress.Domains)
	var suffixes []string
	for _, name := range egress.PrivateDomains {
		if apex, ok := strings.CutPrefix(name, "*."); ok {
			suffixes = append(suffixes, apex)
		} else {
			named = append(named, name)
			allow(name, "", "")
		}
	}
	var endpoints [][2]string
	for _, endpoint := range egress.Endpoints {
		host, p, err := sandbox.SplitEndpoint(endpoint)
		ip, ipErr := netip.ParseAddr(host)
		switch {
		case err != nil || (ipErr != nil && !sandbox.ValidDomain(host)):
			fmt.Fprintf(report, "unsupported %s\n", endpoint)
		case ipErr == nil && isPrivate(ip.Unmap()):
		default:
			named = append(named, host)
			endpoints = append(endpoints, [2]string{host, strconv.Itoa(p)})
		}
	}
	if egress.Public {
		allow("public", "", "")
	}
	for _, apex := range suffixes {
		if !slices.Contains(named, apex) {
			deny(apex)
		}
	}
	for _, apex := range suffixes {
		allow("."+apex, "", "")
	}
	deny("private")
	if !egress.Public {
		for _, e := range endpoints {
			allow(e[0], "tcp", e[1])
		}
		for _, name := range egress.Domains {
			allow(name, "", "")
		}
	}
	return n, nil
}

// addresses returns the sorted, unmapped addresses of a host name or literal.
func addresses(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	found, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(found) == 0 {
		return nil, fmt.Errorf("host did not resolve")
	}
	result := make([]netip.Addr, 0, len(found))
	for _, a := range found {
		result = append(result, a.Unmap().WithZone(""))
	}
	slices.SortFunc(result, netip.Addr.Compare)
	return slices.Compact(result), nil
}
