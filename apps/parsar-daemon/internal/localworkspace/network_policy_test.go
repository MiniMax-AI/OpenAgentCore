package localworkspace

import (
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
	"testing"
)

func TestRuntimeNetworkPolicyMustMatchExecutionButNotReadOnly(t *testing.T) {
	for _, deployed := range []string{"", "enabled", "disabled"} {
		for _, requested := range []string{"", "enabled", "disabled", "restricted"} {
			b, req := testBinding(t)
			b.networkAccess = deployed
			req.LocalEnvironment.NetworkAccess = requested
			_, err := b.Configure(req)
			if (err == nil) != (deployed != "" && deployed == requested) {
				t.Fatalf("execution policy %q/%q: %v", deployed, requested, err)
			}
			req.WorkspaceReadOnly = true
			req.LocalEnvironment = &proto.LocalEnvironment{ID: b.environment}
			if _, err := b.Configure(req); err != nil {
				t.Fatal("read-only operation requires unrelated execution policy", err)
			}
		}
	}
}

func TestRestrictedPolicyBindingCannotBeChangedByARequest(t *testing.T) {
	b, req := testBinding(t)
	b.networkAccess, b.allowedDomains = "restricted", []string{"example.com", "api.example.com"}
	for _, domains := range [][]string{{"api.example.com", "EXAMPLE.com", "example.com"}, {"example.com"}, {"other.example.com"}, nil} {
		req.LocalEnvironment.NetworkAccess = "restricted"
		req.LocalEnvironment.AllowedDomains = domains
		_, err := b.Configure(req)
		if (err == nil) != (len(domains) == 3) {
			t.Fatalf("binding changed by domains %v: %v", domains, err)
		}
	}
	copy := b.NetworkPolicy()
	copy.AllowedDomains[0] = "other.example.com"
	if !b.NetworkPolicy().Equal(agentnetwork.Policy{Access: "restricted", AllowedDomains: []string{"example.com", "api.example.com"}}) {
		t.Fatal("caller mutated frozen policy")
	}
	req.WorkspaceReadOnly = true
	req.LocalEnvironment = &proto.LocalEnvironment{ID: b.environment}
	if _, err := b.Configure(req); err != nil {
		t.Fatal("read requires execution network", err)
	}
	req.LocalEnvironment.AllowedDomains = []string{"other.example.com"}
	if _, err := b.Configure(req); err == nil {
		t.Fatal("read accepted a conflicting supplied policy")
	}
}

func TestRuntimeNetworkPolicyRejectsMalformedDeploymentInput(t *testing.T) {
	for _, tc := range []struct {
		access, domains string
		valid           bool
	}{
		{"restricted", `["example.com"]`, true},
		{"restricted", `[]`, false},
		{"restricted", `["*"]`, false},
		{"restricted", `"example.com"`, false},
		{"enabled", `["example.com"]`, false},
		{"", `["example.com"]`, false},
		{"disabled", `[]`, true},
	} {
		t.Setenv("PARSAR_RUNTIME_NETWORK_ACCESS", tc.access)
		t.Setenv("PARSAR_RUNTIME_ALLOWED_DOMAINS", tc.domains)
		if _, err := RuntimeNetworkPolicy(); (err == nil) != tc.valid {
			t.Fatalf("%s/%s: %v", tc.access, tc.domains, err)
		}
	}
}
