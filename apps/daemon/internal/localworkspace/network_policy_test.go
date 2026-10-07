package localworkspace

import "testing"

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
		t.Setenv("OAC_RUNTIME_NETWORK_ACCESS", tc.access)
		t.Setenv("OAC_RUNTIME_ALLOWED_DOMAINS", tc.domains)
		if _, err := RuntimeNetworkPolicy(); (err == nil) != tc.valid {
			t.Fatalf("%s/%s: %v", tc.access, tc.domains, err)
		}
	}
}
