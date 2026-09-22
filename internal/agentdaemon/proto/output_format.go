package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
)

// ValidateBinary64Schema is an adapter restriction for runtimes that deserialize
// schemas through IEEE-754 JSON numbers. It never rewrites the caller's schema.
// Other adapters need not apply this restriction.
func ValidateBinary64Schema(raw json.RawMessage) error {
	fail := errors.New("This runtime requires an object schema with lossless JSON numbers.")
	var root struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Type != "object" {
		return fail
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail
		}
		number, ok := token.(json.Number)
		if !ok {
			continue
		}
		value, err := number.Float64()
		if err != nil {
			return fail
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fail
		}
		before, ok := new(big.Rat).SetString(number.String())
		if !ok {
			return fail
		}
		after, ok := new(big.Rat).SetString(string(encoded))
		if !ok || before.Cmp(after) != 0 {
			return fail
		}
	}
	return nil
}
