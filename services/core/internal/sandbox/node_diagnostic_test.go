package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
)

func TestNodeDiagnosticContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/node-diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture []string
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, diagnostic := range nodeDiagnostics {
		codes = append(codes, string(diagnostic.code))
		if got := NodeDiagnostic(fmt.Errorf("private probe detail: %w", diagnostic.err)); got != string(diagnostic.code) {
			t.Errorf("wrapped readiness cause = %q, want %q", got, string(diagnostic.code))
		}
		if got := NormalizeNodeDiagnostic(string(diagnostic.code)); got != string(diagnostic.code) {
			t.Errorf("normalized readiness cause = %q, want %q", got, string(diagnostic.code))
		}
	}
	slices.Sort(codes)
	slices.Sort(fixture)
	if !slices.Equal(codes, fixture) {
		t.Fatalf("node diagnostic fixture differs from the authored Go rule: got %v, want %v", fixture, codes)
	}
	if NodeDiagnostic(nil) != "" || NormalizeNodeDiagnostic("") != "" {
		t.Fatal("ready state must have no diagnostic")
	}
	if NodeDiagnostic(errors.New("private probe detail")) != string(NodeProviderUnavailable) || NormalizeNodeDiagnostic("future_code") != string(NodeProviderUnavailable) {
		t.Fatal("unknown causes must remain provider_unavailable")
	}
}
