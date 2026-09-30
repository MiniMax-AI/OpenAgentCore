package oauthrefresh

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func parseEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrRefresh
	}
	return u, nil
}

func endpointAddress(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

type networkPolicy struct {
	privateAddresses map[string]bool
	lookup           func(context.Context, string) ([]net.IPAddr, error)
	dial             func(context.Context, string, string) (net.Conn, error)
}

func newNetworkPolicy(origins []string) (*networkPolicy, error) {
	p := &networkPolicy{privateAddresses: map[string]bool{}, lookup: net.DefaultResolver.LookupIPAddr,
		dial: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext}
	for _, raw := range origins {
		u, err := parseEndpoint(raw)
		if err != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery {
			return nil, errors.New("invalid OAuth trusted origin configuration")
		}
		p.privateAddresses[endpointAddress(u)] = true
	}
	return p, nil
}

func (p *networkPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrRefresh
	}
	addresses, err := p.lookup(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrRefresh
	}
	trusted := p.privateAddresses[net.JoinHostPort(strings.ToLower(host), port)]
	// Validate the complete answer before dialing any address. Use the resolved IP
	// directly so a second DNS lookup cannot switch a public hostname to an internal
	// service after validation. HTTPS still verifies the original hostname.
	for _, item := range addresses {
		ip, ok := netip.AddrFromSlice(item.IP)
		if !ok || item.Zone != "" || (!trusted && !publicAddress(ip.Unmap())) {
			return nil, ErrRefresh
		}
	}
	for _, item := range addresses {
		conn, err := p.dial(ctx, network, net.JoinHostPort(item.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, ErrRefresh
}

var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

func publicAddress(ip netip.Addr) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !sharedAddressSpace.Contains(ip)
}
