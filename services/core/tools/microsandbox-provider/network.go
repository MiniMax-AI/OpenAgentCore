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
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// resolver looks a host name up with the node host's DNS.
type resolver func(ctx context.Context, host string) ([]netip.Addr, error)

func hostResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

func isPrivate(a netip.Addr) bool { return a.IsPrivate() || carrierGradeNAT.Contains(a) }

// resolveTimeout bounds all lookups of one egressNetwork call together.
const resolveTimeout = 5 * time.Second

// egressNetwork builds one allocation's network policy; Create and Resume call
// it with the same Egress, and restore tells which. Rules are first match with
// default deny:
//  1. host DNS
//  2. deny loopback, link-local and metadata
//  3. each address of the Core host, on its port
//  4. each private address that a PrivateDomains name resolves to, without
//     port, because only a port-less address allow lets the guest's DNS answer
//     pass rebind protection, which stays at its default; Endpoints never
//     yield private addresses
//  5. deny every other private address
//  6. unless Public, a tcp allow on its port for each Endpoint (domain, or a
//     public IP literal), and a port-less domain allow for each PrivateDomains
//     and Domains name; only public answers reach a domain allow, since rule 5
//     and rebind protection already refuse private ones; on restore the same
//     allows for the public addresses these names resolve to now come first,
//     since a domain allow that matches an address first refuses it
//  7. public addresses, when Public
//
// Strict hostname enforcement accepts a domain allow over TLS only with TLS
// interception, so create turns it off. Restore cannot (the SDK takes only the
// policy there) and the domain allows of rule 6 no longer pass HTTPS, hence the
// address allows, which miss later DNS changes. They go once restore can turn
// strict enforcement off.
//
// Core, PrivateDomains and, on restore, the rule 6 names are resolved together
// under one deadline. A name that does not resolve gets no address rule and is
// reported to report.
func egressNetwork(ctx context.Context, coreURL string, egress sandbox.Egress, restore bool, resolve resolver, report io.Writer) (*sdk.NetworkConfig, error) {
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
	names := slices.Concat(egress.PrivateDomains, egress.Domains)
	slices.Sort(names)
	names = slices.Compact(names)
	pin := restore && !egress.Public
	lookups := slices.Clone(egress.PrivateDomains)
	if pin {
		lookups = slices.Concat(names, endpointDomains(egress.Endpoints))
	}
	slices.Sort(lookups)
	lookups = slices.Compact(lookups)

	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	var wg sync.WaitGroup
	var coreAddresses []netip.Addr
	var coreErr error
	wg.Go(func() { coreAddresses, coreErr = addresses(ctx, core.Hostname(), resolve) })
	found := make([][]netip.Addr, len(lookups))
	failed := make([]error, len(lookups))
	for i, name := range lookups {
		wg.Go(func() { found[i], failed[i] = addresses(ctx, name, resolve) })
	}
	wg.Wait()
	if coreErr != nil {
		return nil, coreErr
	}
	var private []string
	public := map[string][]string{}
	for i, name := range lookups {
		if failed[i] != nil {
			fmt.Fprintf(report, "unresolved %s\n", name)
			continue
		}
		for _, a := range found[i] {
			if isPrivate(a) {
				if slices.Contains(egress.PrivateDomains, name) {
					private = append(private, a.String())
				}
			} else if pin && a.IsGlobalUnicast() {
				public[name] = append(public[name], a.String())
			}
		}
	}
	slices.Sort(private)
	private = slices.Compact(private)

	n := &sdk.NetworkConfig{DefaultEgress: sdk.PolicyActionDeny, DefaultIngress: sdk.PolicyActionDeny, DisableStrict: !restore}
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
		var domains [][2]string
		for _, endpoint := range egress.Endpoints {
			host, p, err := sandbox.SplitEndpoint(endpoint)
			if err != nil {
				fmt.Fprintf(report, "unsupported %s\n", endpoint)
			} else if a, err := netip.ParseAddr(host); err == nil {
				if a = a.Unmap(); !isPrivate(a) {
					allow(a.String(), "tcp", strconv.Itoa(p))
				}
			} else if sandbox.ValidDomain(host) {
				for _, a := range public[host] {
					allow(a, "tcp", strconv.Itoa(p))
				}
				domains = append(domains, [2]string{host, strconv.Itoa(p)})
			} else {
				fmt.Fprintf(report, "unsupported %s\n", endpoint)
			}
		}
		for _, name := range names {
			for _, a := range public[name] {
				allow(a, "", "")
			}
		}
		for _, d := range domains {
			allow(d[0], "tcp", d[1])
		}
		for _, name := range names {
			allow(name, "", "")
		}
	}
	if egress.Public {
		allow("public", "", "")
	}
	return n, nil
}

// endpointDomains returns the host names of the Endpoints that name one.
func endpointDomains(endpoints []string) []string {
	var hosts []string
	for _, endpoint := range endpoints {
		if host, _, err := sandbox.SplitEndpoint(endpoint); err == nil && sandbox.ValidDomain(host) {
			hosts = append(hosts, host)
		}
	}
	return hosts
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
