package mcode

import "testing"

func TestWorkspaceRejectsInnerNetworkIsolation(t *testing.T) {
	for _, access := range []string{"disabled", "restricted"} {
		c, req, _ := workspaceFixture(t)
		c.Network = access
		req.LocalEnvironment.NetworkAccess = access
		if _, err := prepareWorkspaceOptions(t.Context(), c, req); err == nil {
			t.Fatal("unsupported network isolation accepted")
		}
	}
}
