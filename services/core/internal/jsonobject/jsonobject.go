// Package jsonobject normalizes stored JSON object documents.
package jsonobject

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// ErrInvalid reports a value that is not exactly one JSON object.
var ErrInvalid = errors.New("value must be exactly one JSON object")

// Normalize returns one stable encoding of a JSON object: an empty value
// becomes {}, members are sorted by name, insignificant whitespace is removed
// and numbers keep their literal text. Anything other than a single object,
// including null and trailing values, fails with ErrInvalid.
//
// Objects with the same members and number literals therefore encode
// identically, which stored snapshots and retry identities rely on. It is not
// a general canonical JSON form: strings keep encoding/json's escaping,
// numbers are not rewritten (1 and 1.0 differ) and a repeated member keeps
// its last value.
func Normalize(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrInvalid
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return nil, ErrInvalid
	}
	return normalized, nil
}
