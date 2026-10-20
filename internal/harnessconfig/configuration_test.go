package harnessconfig

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func testDeclaration(kind string, configuration Configuration) Declaration {
	return Declaration{Kind: kind, Label: kind, Configuration: configuration, Capabilities: proto.UnsupportedCapabilities(),
		Placements: []string{"none"}, MCPOrigins: []string{}, WhitespaceOnlyText: proto.CapabilityUnsupported, Execution: ExecutionOptions{TextVerbosities: []string{"medium"}}}
}

func TestRegistryOwnsDeclarations(t *testing.T) {
	declaration := testDeclaration("additional-adapter", Configuration{Providers: []Provider{{Protocol: "responses"}}})
	registry := NewRegistry(declaration)
	declaration.Providers[0].Protocol = "changed"
	got, ok := registry.Lookup("additional-adapter")
	if !ok || got.ValidateProtocol("responses") != nil || got.ValidateProtocol("unknown") == nil {
		t.Fatal("registry did not preserve adapter rules")
	}
	got.Providers[0].Protocol = "mutated"
	again, _ := registry.Lookup("additional-adapter")
	if again.ValidateProtocol("responses") != nil {
		t.Fatal("lookup mutated the registered declaration")
	}
	if _, ok := registry.Lookup("unknown"); ok || registry.SupportsProtocol("changed") || !registry.SupportsProtocol("responses") {
		t.Fatal("unknown declarations were inferred")
	}
}

func TestUndeclaredHarnessHasNoNativeParameters(t *testing.T) {
	registry := NewRegistry()
	for _, raw := range []string{"", "{}", "{ \n }"} {
		if err := registry.ValidateHarnessConfig("additional-adapter", []byte(raw)); err != nil {
			t.Fatalf("empty parameters rejected: %v", err)
		}
	}
	for _, raw := range []string{"null", "[]", `{"effort":"low"}`, `{"effort":"low","effort":"high"}`} {
		if err := registry.ValidateHarnessConfig("additional-adapter", []byte(raw)); err == nil {
			t.Fatal("undeclared or malformed parameters accepted")
		}
	}
	if registry.ValidateProtocol("additional-adapter", "responses") == nil {
		t.Fatal("empty parameters inferred provider support")
	}
}

func TestDeclarationRequiresExplicitSupport(t *testing.T) {
	valid := testDeclaration("example", Configuration{})
	if err := valid.ValidateDeclaration(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Declaration){
		"capability":   func(d *Declaration) { d.Capabilities.Streaming = proto.CapabilityUnspecified },
		"whitespace":   func(d *Declaration) { d.WhitespaceOnlyText = proto.CapabilityUnspecified },
		"placements":   func(d *Declaration) { d.Placements = nil },
		"origins":      func(d *Declaration) { d.MCPOrigins = nil },
		"service MCP":  func(d *Declaration) { d.Placements, d.MCPOrigins = []string{"self_hosted"}, []string{"service"} },
		"bearer":       func(d *Declaration) { d.Capabilities.MCPHTTPBearerAuth = proto.CapabilitySupported },
		"label":        func(d *Declaration) { d.Label = " " },
		"unknown kind": func(d *Declaration) { d.Kind = " example" },
		"verbosity":    func(d *Declaration) { d.Execution.TextVerbosities = []string{"high"} },
	} {
		declaration := valid.Clone()
		change(&declaration)
		if declaration.ValidateDeclaration() == nil {
			t.Errorf("%s: ambiguous declaration accepted", name)
		}
	}
}
