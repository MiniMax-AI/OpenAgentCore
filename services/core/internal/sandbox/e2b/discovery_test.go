package e2b

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestDiscoveryUsesTransientConnectionAndChecksHelper(t *testing.T) {
	caller := &fakeCaller{response: Response{Version: ProtocolVersion, Templates: []TemplateSummary{{ID: "tpl_123", Names: []string{"test"}}}}}
	_, err := Discover(t.Context(), caller, "/tmp/helper", "private-key", "https://sandbox.sandbase.ai", "sandbox.sandbase.ai", "")
	if err != nil || len(caller.requests) != 1 || caller.requests[0].Operation != "list_templates" || caller.requests[0].Config.APIKey != "private-key" {
		t.Fatal(err, caller.requests)
	}
	buildID := uuid.NewString()
	caller.response = Response{Version: ProtocolVersion, Builds: []ReadyBuild{{ID: buildID, CPUs: 2, MemoryMiB: 2048}}}
	_, err = Discover(t.Context(), caller, "/tmp/helper", "private-key", "", "", "tpl_123")
	if err != nil || caller.requests[1].Operation != "list_builds" || caller.requests[1].Config.Template != "tpl_123" {
		t.Fatal(err)
	}
	for _, input := range []struct{ key, url, domain, template string }{
		{"key\nsecret", "", "", ""},
		{"key", "http://localhost", "localhost", ""},
		{"key", "", "", "../template"},
	} {
		if _, err := Discover(t.Context(), caller, "/tmp/helper", input.key, input.url, input.domain, input.template); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatal(err)
		}
	}
	caller.err = errors.New("private-key in provider diagnostic")
	if _, err := Discover(t.Context(), caller, "/tmp/helper", "private-key", "", "", ""); !errors.Is(err, sandbox.ErrComputeUnconfirmed) || err.Error() == caller.err.Error() {
		t.Fatal(err)
	}
}
