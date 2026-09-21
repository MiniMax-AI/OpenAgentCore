package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRestrictedWorkspacePolicyUsesExactBoundAuthority(t *testing.T) {
	config, req, _ := workspaceFixture(t)
	config.Network = "restricted"
	config.AllowedDomains = []string{"Example.com", "api.example.com", "example.com"}
	req.LocalEnvironment.NetworkAccess = "restricted"
	req.LocalEnvironment.AllowedDomains = []string{"api.example.com", "EXAMPLE.COM"}
	opts, err := prepareWorkspaceOptions(t.Context(), config, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(opts.DataDir, "workspace-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		Network string   `json:"network"`
		Domains []string `json:"allowedDomains"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil || profile.Network != "restricted" || !slices.Equal(profile.Domains, []string{"api.example.com", "example.com"}) {
		t.Fatal("native policy lost bound authority", profile, err)
	}
	for _, domains := range [][]string{{"example.com"}, {"example.org"}, nil} {
		req.LocalEnvironment.AllowedDomains = domains
		if _, err := prepareWorkspaceOptions(t.Context(), config, req); err == nil {
			t.Fatal("different policy entered bound Runtime", domains)
		}
	}
}
