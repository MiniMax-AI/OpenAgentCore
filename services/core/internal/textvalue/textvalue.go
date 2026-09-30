// Package textvalue holds the one error shared by every domain for request text
// that Core cannot store.
package textvalue

import "errors"

// ErrUnstorable reports text that PostgreSQL cannot represent: U+0000 or
// invalid UTF-8 in a text value, or a \u0000 escape in a jsonb value. Nothing
// was stored, and the writes sharing the rejected statement's transaction
// rolled back.
var ErrUnstorable = errors.New("text contains characters that cannot be stored")
