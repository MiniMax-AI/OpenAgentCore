package localworkspace

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/google/uuid"
)

func TestFinalizeRebindsIdenticalRestoredInstallation(t *testing.T) {
	first, _ := testBinding(t)
	source := t.TempDir()
	writeSourceSkill(t, source, "restored")
	input := proto.CapabilitySources{Directories: []string{source}}
	root, unlock, err := first.openCapabilities(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err = first.loadCapabilitySnapshot(root, input, first.capabilityIdentity(), false); err != nil {
		t.Fatal(err)
	}
	// The next Session's directory holds the files restored from the first.
	next, err := NewWithCapabilityDirectory(uuid.NewString(), uuid.NewString(), first.workspace, first.capabilityRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = next.loadCapabilitySnapshot(root, proto.CapabilitySources{}, next.capabilityIdentity(), false); err == nil {
		t.Fatal("different selection accepted")
	}
	if _, err = next.loadCapabilitySnapshot(root, input, next.capabilityIdentity(), false); err != nil {
		t.Fatal(err)
	}
	if manifest, err := agentcapabilities.Load(root); err != nil || manifest.Identity != next.capabilityIdentity() {
		t.Fatal("identity not rebound", err)
	}
}
