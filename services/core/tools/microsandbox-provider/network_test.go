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
	got, _ := rules(t, sandbox.Egress{Public: true, Endpoints: []string{"api.openai.com"}}, map[string][]string{
		"core.example.com": {"172.29.144.1"}, "api.openai.com": {"104.18.1.1"}})
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

func TestPrivateDomainRules(t *testing.T) {
	got, report := rules(t, sandbox.Egress{PrivateDomains: []string{"gitlab.example.com", "gone.example.com"}, Endpoints: []string{"models.example.com"}}, map[string][]string{
		"core.example.com": {"172.29.144.1"}, "gitlab.example.com": {"10.74.2.81", "8.8.8.8"}, "models.example.com": {"203.0.113.9"}})
	want := `allow host udp 53
allow host tcp 53
deny loopback  
deny link-local  
deny metadata  
allow 172.29.144.1 tcp 24443
allow 10.74.2.81  
deny private  
allow gitlab.example.com  
allow gone.example.com  
allow models.example.com  `
	if got != want {
		t.Fatalf("rules:\n%s", got)
	}
	if report != "unresolved gone.example.com\n" {
		t.Fatalf("report %q", report)
	}
}

// Domains admit public answers only: they sit after "deny private" and add no address rule.
func TestDomainRulesFollowDenyPrivate(t *testing.T) {
	got, _ := rules(t, sandbox.Egress{Domains: []string{"pypi.org"}, Endpoints: []string{"models.example.com"}}, map[string][]string{
		"core.example.com": {"172.29.144.1"}, "models.example.com": {"203.0.113.9"}, "pypi.org": {"151.101.0.223"}})
	want := `allow host udp 53
allow host tcp 53
deny loopback  
deny link-local  
deny metadata  
allow 172.29.144.1 tcp 24443
deny private  
allow models.example.com  
allow pypi.org  `
	if got != want {
		t.Fatalf("rules:\n%s", got)
	}
}
