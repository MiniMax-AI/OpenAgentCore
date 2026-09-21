package localworkspace

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
)

func TestCapabilityPathsStayRuntimeOwnedAndDoNotGateReads(t *testing.T) {
	binding, request := testBinding(t)
	request.LocalEnvironment.Skills = []agentcapabilities.InstalledSkill{{RelativeRoot: "caller/private", PackageRoot: "caller"}}
	raw, err := json.Marshal(request.LocalEnvironment)
	if err != nil || bytes.Contains(raw, []byte("caller")) || bytes.Contains(raw, []byte("skills")) {
		t.Fatal("Runtime paths crossed the public daemon descriptor", err)
	}
	configured, err := binding.Configure(request)
	if err != nil || len(configured.LocalEnvironment.Skills) != 0 {
		t.Fatal("execution accepted caller-supplied Skill paths", err)
	}
	request.LocalEnvironment.Capabilities = true
	request.LocalEnvironment.Skills = nil
	request.WorkspaceReadOnly = true
	if _, err := binding.Configure(request); err != nil {
		t.Fatal("read-only binding required a capability installation", err)
	}
}
