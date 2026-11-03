package opensandbox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	osdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func policyFor(t *testing.T, coreURL string, egress sandbox.Egress, resolved ...string) *osdk.NetworkPolicy {
	t.Helper()
	resolve := func(context.Context, string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range resolved {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
	policy, err := networkPolicy(t.Context(), sandbox.Bootstrap{CoreURL: coreURL, Egress: egress}, resolve)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func targets(policy *osdk.NetworkPolicy) []string {
	var out []string
	for _, r := range policy.Egress {
		s := r.Action + " " + r.Target
		for _, p := range r.Ports {
			s += fmt.Sprintf(":%d", p)
		}
		out = append(out, s)
	}
	return out
}

const privateDenies = "deny 10.0.0.0/8,deny 172.16.0.0/12,deny 192.168.0.0/16,deny 100.64.0.0/10,deny 169.254.0.0/16,deny fc00::/7,deny fe80::/10"

func TestPublicNetworkDeniesPrivateRanges(t *testing.T) {
	policy := policyFor(t, "https://core.example/api/v1", sandbox.Egress{Public: true}, "203.0.113.5")
	if policy.DefaultAction != "allow" || strings.Join(targets(policy), ",") != privateDenies {
		t.Fatalf("%s rules = %v", policy.DefaultAction, targets(policy))
	}
}

func TestRestrictedNetworkAllowsOnlyNamedDestinations(t *testing.T) {
	egress := sandbox.Egress{Domains: []string{"Docs.Example.com"}, Endpoints: []string{"api.model.example:443", "203.0.113.9:8443", "10.0.0.7:9000", "api.model.example:8443"}}
	policy := policyFor(t, "https://core.example/api/v1", egress, "203.0.113.5")
	// The private denies win over the private answers of every allowed name.
	want := privateDenies + ",allow core.example,allow api.model.example,allow 203.0.113.9:8443,allow docs.example.com"
	if policy.DefaultAction != "deny" || strings.Join(targets(policy), ",") != want {
		t.Fatalf("%s rules = %v, want %v", policy.DefaultAction, targets(policy), want)
	}
	if policy := policyFor(t, "http://203.0.113.4:8080/api/v1", sandbox.Egress{}); strings.Join(targets(policy), ",") != privateDenies+",allow 203.0.113.4:8080" {
		t.Fatalf("core literal rules = %v", targets(policy))
	}
}

func TestNetworkRejectsPrivateCore(t *testing.T) {
	for name, tc := range map[string]struct {
		url      string
		resolved []string
	}{"literal": {"https://10.1.2.3:8443/api/v1", nil}, "resolved name": {"https://core.example/api/v1", []string{"203.0.113.5", "10.1.2.3"}}} {
		resolve := func(context.Context, string) ([]netip.Addr, error) {
			var out []netip.Addr
			for _, a := range tc.resolved {
				out = append(out, netip.MustParseAddr(a))
			}
			return out, nil
		}
		for _, public := range []bool{true, false} {
			if _, err := networkPolicy(t.Context(), sandbox.Bootstrap{CoreURL: tc.url, Egress: sandbox.Egress{Public: public}}, resolve); !errors.Is(err, sandbox.ErrInvalid) {
				t.Fatalf("%s public=%v: %v", name, public, err)
			}
		}
	}
}

func TestUnresolvableCoreFailsBeforeDispatch(t *testing.T) {
	fail := func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }
	if _, err := networkPolicy(t.Context(), sandbox.Bootstrap{CoreURL: "https://core.example/api/v1", Egress: sandbox.Egress{Public: true}}, fail); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal(err)
	}
}
