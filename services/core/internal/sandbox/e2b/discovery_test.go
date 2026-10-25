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

func TestDiscoveryOptionsMapTemplatesAndBuilds(t *testing.T) {
	templates := discoveryOptions(Response{Templates: []TemplateSummary{{ID: "tpl_1", Names: []string{"web"}}}}, "").Options
	if len(templates) != 1 || templates[0].Value != "tpl_1" || templates[0].Complete || templates[0].Label != "web · tpl_1" {
		t.Fatal(templates)
	}
	build := uuid.NewString()
	builds := discoveryOptions(Response{Builds: []ReadyBuild{{ID: build, CPUs: 2, MemoryMiB: 2048}}}, "tpl_1").Options
	if len(builds) != 1 || builds[0].Value != "tpl_1:"+build || !builds[0].Complete || builds[0].Resources == nil || builds[0].Resources.CPUs != 2 || builds[0].Resources.MemoryMiB != 2048 {
		t.Fatal(builds)
	}
}
