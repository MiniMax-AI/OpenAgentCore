package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func fixedDNS(table map[string][]string) resolver {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		answers, ok := table[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		var out []netip.Addr
		for _, a := range answers {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func rules(t *testing.T, egress sandbox.Egress, dns map[string][]string) (string, string) {
	t.Helper()
	var report bytes.Buffer
	n, err := egressNetwork(context.Background(), "https://core.example.com:24443", egress, fixedDNS(dns), &report)
	if err != nil {
		t.Fatal(err)
	}
	if n.DefaultEgress != "deny" || n.DefaultIngress != "deny" || n.DNS != nil {
		t.Fatalf("defaults or DNS options changed: %+v", n)
	}
	var lines []string
	for _, r := range n.Rules {
		lines = append(lines, fmt.Sprintf("%s %s %s %s", r.Action, r.Destination, r.Protocol, r.Port))
	}
	return strings.Join(lines, "\n"), report.String()
}

func TestDefaultPolicyRules(t *testing.T) {
	got, _ := rules(t, sandbox.Egress{Public: true, Endpoints: []string{"api.openai.com:443"}}, map[string][]string{
		"core.example.com": {"172.29.144.1"}})
	want := `allow host udp 53
allow host tcp 53
deny loopback  
deny link-local  
deny metadata  
allow 172.29.144.1 tcp 24443
deny private  
allow public  `
	if got != want {
		t.Fatalf("rules:\n%s", got)
	}
}

// Only PrivateDomains names yield port-less private allows; an Endpoint is
// limited to its port and sits after "deny private".
func TestEndpointIsPortLimitedAndNeverPrivate(t *testing.T) {
	got, report := rules(t, sandbox.Egress{PrivateDomains: []string{"gitlab.example.com", "gone.example.com"}, Domains: []string{"pypi.org"},
		Endpoints: []string{"models.example.com:8443", "203.0.113.9:8000", "10.1.2.3:8000"}}, map[string][]string{
		"core.example.com": {"172.29.144.1"}, "gitlab.example.com": {"10.74.2.81", "8.8.8.8"}, "models.example.com": {"10.9.9.9"}})
	want := `allow host udp 53
allow host tcp 53
deny loopback  
deny link-local  
deny metadata  
allow 172.29.144.1 tcp 24443
allow 10.74.2.81  
deny private  
allow models.example.com tcp 8443
allow 203.0.113.9 tcp 8000
allow gitlab.example.com  
allow gone.example.com  
allow pypi.org  `
	if got != want {
		t.Fatalf("rules:\n%s", got)
	}
	if report != "unresolved gone.example.com\n" {
		t.Fatalf("report %q", report)
	}
}
