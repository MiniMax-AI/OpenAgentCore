package echotext

import (
	"strings"
	"testing"
)

func TestAllowed(t *testing.T) {
	for value, want := range map[string]bool{
		"not-a-valid-id":         true,
		"skill_é":                true,
		strings.Repeat("x", 256): true,
		strings.Repeat("x", 257): false,
		"bad\x01value":           false,
		"line\nbreak":            false,
		"\xff":                   false,
		"replacement�":           false,
	} {
		if got := Allowed(value); got != want {
			t.Errorf("Allowed(%q) = %t; want %t", value, got, want)
		}
	}
}
