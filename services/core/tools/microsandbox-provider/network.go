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
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// resolver looks a host name up with the node host's DNS.
type resolver func(ctx context.Context, host string) ([]netip.Addr, error)

func hostResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

func isPrivate(a netip.Addr) bool { return a.IsPrivate() || carrierGradeNAT.Contains(a) }

// egressNetwork builds one allocation's network policy; Create and Resume call
// it with the same Egress. Rules are first match with default deny:
//  1. host DNS
//  2. deny loopback, link-local and metadata
//  3. each address of the Core host, on its port
//  4. each private address that an Endpoint or PrivateDomains name resolves to,
//     without port, because only a port-less address allow lets the guest's DNS
//     answer pass rebind protection, which stays at its default
//  5. deny every other private address
//  6. unless Public, a domain allow for each Endpoint and PrivateDomains name,
//     so their public answers work
//  7. public addresses, when Public
//
// A name that does not resolve gets no address rule and is reported to report.
func egressNetwork(ctx context.Context, coreURL string, egress sandbox.Egress, resolve resolver, report io.Writer) (*sdk.NetworkConfig, error) {
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
	coreAddresses, err := addresses(ctx, core.Hostname(), resolve)
	if err != nil {
		return nil, err
	}
	names := slices.Concat(egress.Endpoints, egress.PrivateDomains)
	slices.Sort(names)
	names = slices.Compact(names)

	var private []string
	for _, name := range names {
		found, err := addresses(ctx, name, resolve)
		if err != nil {
			fmt.Fprintf(report, "unresolved %s\n", name)
			continue
		}
		for _, a := range found {
			if isPrivate(a) {
				private = append(private, a.String())
			}
		}
	}
	slices.Sort(private)
	private = slices.Compact(private)

	n := &sdk.NetworkConfig{DefaultEgress: sdk.PolicyActionDeny, DefaultIngress: sdk.PolicyActionDeny}
	allow := func(destination, protocol, port string) {
		n.Rules = append(n.Rules, sdk.PolicyRule{Action: sdk.PolicyActionAllow, Direction: sdk.PolicyDirectionEgress, Destination: destination, Protocol: sdk.PolicyProtocol(protocol), Port: port})
	}
	deny := func(destination string) {
		n.Rules = append(n.Rules, sdk.PolicyRule{Action: sdk.PolicyActionDeny, Direction: sdk.PolicyDirectionEgress, Destination: destination})
	}
	allow("host", "udp", "53")
	allow("host", "tcp", "53")
	for _, group := range []string{"loopback", "link-local", "metadata"} {
		deny(group)
	}
	for _, a := range coreAddresses {
		allow(a.String(), "tcp", port)
	}
	for _, a := range private {
		allow(a, "", "")
	}
	deny("private")
	if !egress.Public {
		for _, name := range names {
			if a, err := netip.ParseAddr(name); err == nil {
				allow(a.Unmap().String(), "", "")
			} else if sandbox.ValidDomain(name) {
				allow(name, "", "")
			} else {
				fmt.Fprintf(report, "unsupported %s\n", name)
			}
		}
	}
	if egress.Public {
		allow("public", "", "")
	}
	return n, nil
}

// addresses returns the sorted, unmapped addresses of a host name or literal.
func addresses(ctx context.Context, host string, resolve resolver) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	found, err := resolve(ctx, host)
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
