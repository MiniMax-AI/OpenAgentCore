package harnessconfig

import "testing"

func TestRegistryOwnsDeclarations(t *testing.T) {
	declaration := Configuration{Providers: []Provider{{Protocol: "fixture", RequiresTokenLimits: true}}}
	registry := NewRegistry(map[string]Configuration{"additional-adapter": declaration})
	declaration.Providers[0].Protocol = "changed"
	got, ok := registry.Lookup("additional-adapter")
	if !ok || got.Validate("fixture", 100, 20) != nil || got.Validate("fixture", 0, 0) == nil || got.ValidateProtocol("unknown") == nil {
		t.Fatal("registry did not preserve adapter rules")
	}
	got.Providers[0].RequiresTokenLimits = false
	again, _ := registry.Lookup("additional-adapter")
	if again.Validate("fixture", 0, 0) == nil {
		t.Fatal("lookup mutated the registered declaration")
	}
	if _, ok := registry.Lookup("unknown"); ok || registry.SupportsProtocol("changed") || !registry.SupportsProtocol("fixture") {
		t.Fatal("unknown declarations were inferred")
	}
}
