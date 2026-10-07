package sandbox

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

// DecodeConfigurationObject rejects unknown fields, null, nonobjects and trailing
// input without exposing submitted content in its error. Missing objects are empty.
func DecodeConfigurationObject(raw json.RawMessage, target any, allowed ...string) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	for field, value := range fields {
		if !slices.Contains(allowed, field) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
