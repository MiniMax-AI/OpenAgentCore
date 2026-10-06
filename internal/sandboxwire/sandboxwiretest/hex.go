// Package sandboxwiretest reads the hex golden fixtures of the sandbox wire
// protocols.
package sandboxwiretest

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReadHex returns the bytes of testdata/name: hex digits, with whitespace
// ignored and text from # to the end of a line a comment.
func ReadHex(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var digits strings.Builder
	for line := range strings.Lines(string(raw)) {
		line, _, _ = strings.Cut(line, "#")
		digits.WriteString(strings.Join(strings.Fields(line), ""))
	}
	b, err := hex.DecodeString(digits.String())
	if err != nil {
		t.Fatal(err)
	}
	return b
}
