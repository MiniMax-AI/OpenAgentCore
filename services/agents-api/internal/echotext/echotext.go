// Package echotext decides whether an error message may repeat a
// caller-supplied value.
package echotext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Allowed bounds a caller-supplied value that an error message repeats: at most
// 256 bytes of valid, printable UTF-8. JSON escaping can grow each byte sixfold.
// encoding/json replaces invalid bytes and lone surrogates with U+FFFD, so a
// value containing it is not repeated either.
func Allowed(value string) bool {
	if len(value) > 256 || !utf8.ValidString(value) || strings.ContainsRune(value, utf8.RuneError) {
		return false
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
