package proto

import "testing"

func TestBinary64SchemaPreservesNumericMeaning(t *testing.T) {
	for _, s := range []string{
		`{"type":"object","properties":{"n":{"const":9007199254740992}},"const":0.1}`,
		`{"type":"object","const":1e3}`, `{"type":"object","enum":[{"n":-0.0}]}`,
	} {
		if err := ValidateBinary64Schema([]byte(s)); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for _, s := range []string{
		`{"type":"object","properties":{"n":{"const":0}},"const":9007199254740993}`,
		`{"type":"object","const":1.0000000000000001}`, `{"type":"object","const":1e1000}`,
		`{"type":"object","const":1e-1000}`, `{"type":"array"}`, `null`, `{}`,
	} {
		if err := ValidateBinary64Schema([]byte(s)); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
