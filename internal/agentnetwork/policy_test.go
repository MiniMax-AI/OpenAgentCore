package agentnetwork

import (
	"reflect"
	"strings"
	"testing"
)

func TestPolicyValidationRejectsPatternsAndAddressSyntax(t *testing.T) {
	for _, host := range []string{"", "*.example.com", "example.com:443", "https://example.com", "example.com/path", "example.com?x", "example.com#x", "user@example.com", " example.com", "example.com\n", "example..com", "-example.com", "example-.com", "example_com", "127.0.0.1", "::1", "example.com.", "例子.com", strings.Repeat("x", 64) + ".com"} {
		if (Policy{Access: "restricted", AllowedDomains: []string{host}}).Validate() == nil {
			t.Fatalf("accepted an unqualified hostname %q", host)
		}
	}
	for _, policy := range []Policy{{}, {Access: "unknown"}, {Access: "restricted"}, {Access: "enabled", AllowedDomains: []string{"example.com"}}, {Access: "disabled", AllowedDomains: []string{"example.com"}}, {Access: "restricted", AllowedDomains: make([]string, 101)}} {
		if policy.Validate() == nil {
			t.Fatalf("accepted invalid policy %#v", policy)
		}
	}
	for _, policy := range []Policy{{Access: "enabled"}, {Access: "disabled"}, {Access: "restricted", AllowedDomains: []string{"Example.com", "api.example.com", "xn--fsqu00a.com"}}} {
		if err := policy.Validate(); err != nil {
			t.Fatalf("rejected policy %#v: %v", policy, err)
		}
	}
}

func TestTemplateNetworkOverrideCannotBroaden(t *testing.T) {
	policies := []Policy{
		{Access: "enabled"},
		{Access: "disabled"},
		{Access: "restricted", AllowedDomains: []string{"a.example.com", "b.example.com"}},
		{Access: "restricted", AllowedDomains: []string{"A.example.com"}},
		{Access: "restricted", AllowedDomains: []string{"sub.a.example.com"}},
	}
	// Rows are templates; columns are requested effective policies.
	want := [][]bool{{true, true, true, true, true}, {false, true, false, false, false}, {false, true, true, true, false}, {false, true, false, true, false}, {false, true, false, false, true}}
	for i, template := range policies {
		for j, requested := range policies {
			if requested.Narrows(template) != want[i][j] {
				t.Errorf("template %d override %d", i, j)
			}
		}
	}
	if (Policy{Access: "restricted"}).Narrows(policies[0]) || policies[1].Narrows(Policy{}) {
		t.Fatal("invalid policy cannot gain authority through an override")
	}
}

func TestPolicyIdentityPreservesPublicInput(t *testing.T) {
	input := []string{"B.example.com", "a.example.com", "B.example.com"}
	before := append([]string(nil), input...)
	policy := Policy{Access: "restricted", AllowedDomains: input}
	other := Policy{Access: "restricted", AllowedDomains: []string{"a.example.com", "b.example.com"}}
	if !policy.Equal(other) || !reflect.DeepEqual(input, before) {
		t.Fatal("effective comparison changed caller input or list order affected authority")
	}
	if policy.Equal(Policy{Access: "restricted", AllowedDomains: []string{"example.com"}}) {
		t.Fatal("parent hostname must not grant subdomain authority")
	}
}
