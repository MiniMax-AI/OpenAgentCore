package providers

import "testing"

// FixtureRegistry returns the built-in registry with one more adapter. It
// exists only in this package's test binary, never in Core.
func FixtureRegistry(t *testing.T, kind string, adapter Adapter) *Registry {
	t.Helper()
	registry := Builtin()
	if _, ok := registry.adapters[kind]; ok {
		t.Fatal("fixture replaces existing registration")
	}
	registry.adapters[kind] = adapter
	return registry
}
