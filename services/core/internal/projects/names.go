package projects

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Display name limits, in Unicode code points. They are separate from the
// sandbox node name limit, which counts bytes.
const (
	ProjectNameMaxLength = 128
	KeyNameMaxLength     = 80
)

// normalizeName trims surrounding space from an administrator-supplied display
// name. The name must be valid UTF-8, nonempty after trimming, at most max code
// points, and free of control characters anywhere in the submitted value.
func normalizeName(value string, max int) (string, error) {
	control := strings.ContainsFunc(value, unicode.IsControl)
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > max || control {
		return "", &NameError{MaxLength: max}
	}
	return value, nil
}
