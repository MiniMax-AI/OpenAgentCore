package providers

import "testing"

// RegisterFixture exists only in this package's test binary, never in Core.
func RegisterFixture(t *testing.T, kind string, adapter Adapter) {
	t.Helper()
	if _, ok := adapters[kind]; ok {
		t.Fatal("fixture replaces existing registration")
	}
	adapters[kind] = adapter
	t.Cleanup(func() { delete(adapters, kind) })
}
