package localworkspace

import "testing"

func TestCapabilitiesDoNotGateReads(t *testing.T) {
	binding, request := testBinding(t)
	request.WorkspaceReadOnly = true
	request.LocalEnvironment.CapabilitySources = nil
	if err := binding.Configure(request.PromptRequestPayload); err != nil {
		t.Fatal("read-only binding required a capability installation", err)
	}
}
