package agentcapabilities

import "testing"

func TestPortableSourcesAreNotLocalAuthorization(t *testing.T) {
	if ValidateDirectories([]string{`C:\skills`}) == nil || ValidateLocalDirectories([]string{`C:\skills`, `\\host\share\skills`}) == nil {
		t.Fatal("portable source weakened local/managed authorization")
	}
}
