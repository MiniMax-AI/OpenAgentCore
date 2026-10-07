package harnessconfig_test

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestRegistryOwnsDeclarations(t *testing.T) {
	declaration := prototest.ModelConfiguration()
	declaration.Providers[0].RequiresTokenLimits = true
	registry := harnessconfig.NewRegistry(map[string]harnessconfig.Configuration{"additional-adapter": declaration})
	declaration.Providers[0].Protocol = "changed"
	got, ok := registry.Lookup("additional-adapter")
	if !ok || got.Validate("responses", 100, 20) != nil || got.Validate("responses", 0, 0) == nil || got.ValidateProtocol("unknown") == nil {
		t.Fatal("registry did not preserve adapter rules")
	}
	got.Providers[0].RequiresTokenLimits = false
	again, _ := registry.Lookup("additional-adapter")
	if again.Validate("responses", 0, 0) == nil {
		t.Fatal("lookup mutated the registered declaration")
	}
	if _, ok := registry.Lookup("unknown"); ok {
		t.Fatal("unknown declarations were inferred")
	}
}

func TestUndeclaredHarnessHasNoNativeParameters(t *testing.T) {
	registry := harnessconfig.NewRegistry(nil)
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
