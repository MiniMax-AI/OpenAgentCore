package main

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

func rules(t *testing.T, egress sandbox.Egress) (string, string) {
	t.Helper()
	var report bytes.Buffer
	n, err := egressNetwork(context.Background(), "https://172.29.144.1:24443", egress, &report)
	if err != nil {
		t.Fatal(err)
	}
	// Restore takes the config as is and accepts only rules and defaults.
	if want := (sdk.NetworkConfig{DefaultEgress: sdk.PolicyActionDeny, DefaultIngress: sdk.PolicyActionDeny, Rules: n.Rules}); !reflect.DeepEqual(*n, want) {
		t.Fatalf("config carries more than rules and defaults: %+v", n)
	}
	var lines []string
	for _, r := range n.Rules {
		lines = append(lines, fmt.Sprintf("%s %s %s %s", r.Action, r.Destination, r.Protocol, r.Port))
	}
	return strings.Join(lines, "\n"), report.String()
}

const head = `allow host udp 53
allow host tcp 53
deny loopback  
deny link-local  
deny metadata  
deny host  
deny * udp 53
deny * tcp 53
allow 172.29.144.1 tcp 24443
`

func TestDefaultPolicyRules(t *testing.T) {
	got, _ := rules(t, sandbox.Egress{Public: true, Endpoints: []string{"api.openai.com:443"}})
	if want := head + "allow public  \ndeny private  "; got != want {
		t.Fatalf("rules:\n%s", got)
	}
}

// Exact cap names are port-less allows before "deny private"; an Endpoint is
// limited to its port, a private IP literal is dropped, a bad one is reported.
func TestNotPublicRules(t *testing.T) {
	got, report := rules(t, sandbox.Egress{PrivateDomains: []string{"gitlab.example.com"}, Domains: []string{"pypi.org"},
		Endpoints: []string{"models.example.com:8443", "203.0.113.9:8000", "10.1.2.3:8000", "bad host:1"}})
	want := head + `allow gitlab.example.com  
deny private  
allow models.example.com tcp 8443
allow 203.0.113.9 tcp 8000
allow pypi.org  `
	if got != want || report != "unsupported bad host:1\n" {
		t.Fatalf("rules:\n%s\nreport %q", got, report)
	}
}

// A suffix rule matches the apex too, so each "*.apex" entry denies the apex
// first unless this Egress allows the apex by name; public comes before it.
func TestSuffixRules(t *testing.T) {
	cases := []struct {
		name   string
		egress sandbox.Egress
		want   string
	}{
		{"restricted", sandbox.Egress{Domains: []string{"example.com"}, PrivateDomains: []string{"*.corp.example", "git.corp.example"}},
			head + "allow git.corp.example  \ndeny corp.example  \nallow .corp.example  \ndeny private  \nallow example.com  "},
		{"enabled", sandbox.Egress{Public: true, PrivateDomains: []string{"*.corp.example", "git.corp.example", "llm.corp.example"}, Endpoints: []string{"api.minimaxi.com:443"}},
			head + "allow git.corp.example  \nallow llm.corp.example  \nallow public  \ndeny corp.example  \nallow .corp.example  \ndeny private  "},
		{"apex listed", sandbox.Egress{PrivateDomains: []string{"*.corp.example", "corp.example"}},
			head + "allow corp.example  \nallow .corp.example  \ndeny private  "},
		{"apex is a model host", sandbox.Egress{PrivateDomains: []string{"*.corp.example"}, Endpoints: []string{"corp.example:8000"}},
			head + "allow .corp.example  \ndeny private  \nallow corp.example tcp 8000"},
	}
	for _, tc := range cases {
		if got, _ := rules(t, tc.egress); got != tc.want {
			t.Fatalf("%s:\n%s", tc.name, got)
		}
	}
}
