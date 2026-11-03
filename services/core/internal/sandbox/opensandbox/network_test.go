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

func TestPublicNetworkDeniesPrivateRangesExceptCore(t *testing.T) {
	for name, tc := range map[string]struct {
		url      string
		resolved []string
	}{"literal": {"https://10.1.2.3:8443/api/v1", nil}, "resolved name": {"https://core.example/api/v1", []string{"10.1.2.3"}}} {
		t.Run(name, func(t *testing.T) {
			policy := policyFor(t, tc.url, sandbox.Egress{Public: true}, tc.resolved...)
			if policy.DefaultAction != "allow" {
				t.Fatal(policy.DefaultAction)
			}
			core := netip.MustParseAddr("10.1.2.3")
			var covered []string
			for _, r := range policy.Egress {
				if r.Action != "deny" || len(r.Ports) != 0 {
					t.Fatalf("rule %+v", r)
				}
				if netip.MustParsePrefix(r.Target).Contains(core) {
					t.Fatalf("deny %s covers Core", r.Target)
				}
				covered = append(covered, r.Target)
			}
			// The other private ranges stay denied whole, and the carve-out leaves a sibling of Core's /32.
			joined := " " + strings.Join(covered, " ") + " "
			for _, want := range []string{" 172.16.0.0/12 ", " 192.168.0.0/16 ", " 100.64.0.0/10 ", " 169.254.0.0/16 ", " fc00::/7 ", " fe80::/10 ", " 10.1.2.2/32 ", " 10.128.0.0/9 "} {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing %q in %s", want, joined)
				}
			}
		})
	}
}

func TestPublicNetworkLeavesPublicCoreUntouched(t *testing.T) {
	policy := policyFor(t, "https://core.example/api/v1", sandbox.Egress{Public: true}, "203.0.113.5")
	if len(policy.Egress) != len(privatePrefixes) || policy.Egress[0].Target != "10.0.0.0/8" {
		t.Fatalf("rules = %v", targets(policy))
	}
}

func TestRestrictedNetworkAllowsOnlyNamedDestinations(t *testing.T) {
	egress := sandbox.Egress{Domains: []string{"Docs.Example.com"}, Endpoints: []string{"api.model.example:443", "203.0.113.9:8443", "10.0.0.7:9000", "api.model.example:8443"}}
	got := targets(policyFor(t, "https://core.example/api/v1", egress))
	want := []string{"allow core.example", "allow api.model.example", "allow 203.0.113.9:8443", "allow docs.example.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	if policy := policyFor(t, "http://10.2.3.4:8080/api/v1", sandbox.Egress{}); policy.DefaultAction != "deny" || strings.Join(targets(policy), ",") != "allow 10.2.3.4:8080" {
		t.Fatalf("core literal rules = %v", targets(policy))
	}
}

func TestUnresolvableCoreFailsBeforeDispatch(t *testing.T) {
	fail := func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }
	if _, err := networkPolicy(t.Context(), sandbox.Bootstrap{CoreURL: "https://core.example/api/v1", Egress: sandbox.Egress{Public: true}}, fail); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal(err)
	}
}
