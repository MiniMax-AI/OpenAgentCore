package environmentconfig

import (
	"bytes"
	"encoding/json"
)

// Decode reads stored configuration JSON into out. It rejects removed
// configuration fields instead of silently dropping them, and fails with
// ErrInvalid for invalid JSON, unknown fields or a mismatched shape.
func Decode(data []byte, out any) error {
	if !json.Valid(data) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ErrInvalid
	}
	return nil
}
