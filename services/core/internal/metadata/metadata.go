// Package metadata owns the rules for resource metadata: string key and value
// pairs that Agents, Sessions and Vaults carry for their applications.
package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxPairs       = 16
	maxKeyLength   = 64
	maxValueLength = 512
	maxEncoded     = 64 * 1024
)

// Kind names the rule a metadata map breaks.
type Kind int

const (
	// TooManyPairs: more than 16 pairs; Length is the pair count.
	TooManyPairs Kind = iota + 1
	// KeyTooLong: a key longer than 64 characters; Length is its length.
	KeyTooLong
	// ValueTooLong: a value longer than 512 characters; Length is its length.
	ValueTooLong
	// KeyUnstorable: a key contains U+0000.
	KeyUnstorable
	// ValueUnstorable: a value contains U+0000.
	ValueUnstorable
)

// Violation is the first rule a metadata map breaks. Key is empty for
// TooManyPairs, and Length is the counted size for the length rules.
type Violation struct {
	Kind   Kind
	Key    string
	Length int
}

func (v *Violation) Error() string {
	switch v.Kind {
	case TooManyPairs:
		return fmt.Sprintf("metadata has %d pairs; at most %d are allowed", v.Length, maxPairs)
	case KeyTooLong:
		return fmt.Sprintf("metadata key has %d characters; at most %d are allowed", v.Length, maxKeyLength)
	case ValueTooLong:
		return fmt.Sprintf("metadata value has %d characters; at most %d are allowed", v.Length, maxValueLength)
	case KeyUnstorable:
		return "metadata key contains U+0000"
	case ValueUnstorable:
		return "metadata value contains U+0000"
	}
	return "invalid metadata"
}

// ErrTooLarge reports metadata whose JSON encoding exceeds 64 KiB.
var ErrTooLarge = errors.New("metadata exceeds 64 KiB")

// Validate applies the pinned limits of 16 pairs, 64-character keys and
// 512-character values, then ValidateStorable. Keys are checked in sorted
// order, so the reported violation is stable.
func Validate(metadata map[string]string) error {
	if len(metadata) > maxPairs {
		return &Violation{Kind: TooManyPairs, Length: len(metadata)}
	}
	for _, key := range slices.Sorted(maps.Keys(metadata)) {
		if length := utf8.RuneCountInString(key); length > maxKeyLength {
			return &Violation{Kind: KeyTooLong, Key: key, Length: length}
		}
		if length := utf8.RuneCountInString(metadata[key]); length > maxValueLength {
			return &Violation{Kind: ValueTooLong, Key: key, Length: length}
		}
	}
	return ValidateStorable(metadata)
}

// ValidateStorable applies only the local U+0000 limit: PostgreSQL text and
// jsonb cannot store it, although the official service accepts it. Keys are
// checked in sorted order.
func ValidateStorable(metadata map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(metadata)) {
		if strings.ContainsRune(key, 0) {
			return &Violation{Kind: KeyUnstorable, Key: key}
		}
		if strings.ContainsRune(metadata[key], 0) {
			return &Violation{Kind: ValueUnstorable, Key: key}
		}
	}
	return nil
}

// Encode returns the stored JSON object; nil metadata is stored as {}.
// It enforces only the 64 KiB storage bound, not Validate's rules.
func Encode(metadata map[string]string) ([]byte, error) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxEncoded {
		return nil, ErrTooLarge
	}
	return encoded, nil
}
