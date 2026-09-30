package e2b

import (
	"strings"
	"testing"
)

func TestNormalizeEndpoint(t *testing.T) {
	for _, tc := range []struct {
		apiURL, domain string
		valid          bool
	}{
		{"", "", true},
		{"https://sandbox-test.sandbase.ai", "sandbox-test.sandbase.ai", true},
		{"https://api.e2b.app", "e2b.app", true},
		{"https://api.sandbox-test.sandbase.ai", "sandbox-test.sandbase.ai", true},
		{"https://unrelated.example", "sandbox-test.sandbase.ai", false},
		{"https://sandbox-test.sandbase.ai", "", false},
		{"", "sandbox-test.sandbase.ai", false},
		{"http://sandbox-test.sandbase.ai", "sandbox-test.sandbase.ai", false},
		{"https://sandbox-test.sandbase.ai/path", "sandbox-test.sandbase.ai", false},
		{"https://sandbox-test.sandbase.ai:443", "sandbox-test.sandbase.ai", false},
		{"https://127.0.0.1", "sandbox-test.sandbase.ai", false},
		{"https://localhost", "sandbox-test.sandbase.ai", false},
		{"https://-bad.example", "sandbox-test.sandbase.ai", false},
		{"https://good.example", "bad..example", false},
		{"https://" + strings.Repeat("a.", 125) + "example", "good.example", false},
	} {
		apiURL, domain, err := NormalizeEndpoint(tc.apiURL, tc.domain)
		if (err == nil) != tc.valid {
			t.Errorf("NormalizeEndpoint(%q, %q): unexpected error %v", tc.apiURL, tc.domain, err)
		}
		if tc.apiURL == "" && tc.domain == "" && (apiURL != OfficialAPIURL || domain != OfficialDomain) {
			t.Errorf("official defaults: %q %q", apiURL, domain)
		}
	}
}
