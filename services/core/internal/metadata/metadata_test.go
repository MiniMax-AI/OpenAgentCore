package metadata

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func pairs(n int) map[string]string {
	result := make(map[string]string, n)
	for i := range n {
		result[fmt.Sprintf("k%02d", i)] = "v"
	}
	return result
}

func TestValidate(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata map[string]string
		want     *Violation
	}{
		{"nil", nil, nil},
		{"boundary pairs", pairs(16), nil},
		{"boundary key", map[string]string{strings.Repeat("雪", 64): "v"}, nil},
		{"boundary value", map[string]string{"k": strings.Repeat("雪", 512)}, nil},
		{"too many pairs", pairs(17), &Violation{Kind: TooManyPairs, Length: 17}},
		{"long key", map[string]string{strings.Repeat("K", 65): "v"}, &Violation{Kind: KeyTooLong, Key: strings.Repeat("K", 65), Length: 65}},
		{"long value", map[string]string{"k": strings.Repeat("雪", 513)}, &Violation{Kind: ValueTooLong, Key: "k", Length: 513}},
		{"sorted keys", map[string]string{"b": strings.Repeat("x", 513), "a": strings.Repeat("x", 514)}, &Violation{Kind: ValueTooLong, Key: "a", Length: 514}},
		{"key before value", map[string]string{strings.Repeat("K", 65): strings.Repeat("x", 513)}, &Violation{Kind: KeyTooLong, Key: strings.Repeat("K", 65), Length: 65}},
		{"length before U+0000", map[string]string{"a": "\x00", "b": strings.Repeat("x", 513)}, &Violation{Kind: ValueTooLong, Key: "b", Length: 513}},
		{"U+0000 key", map[string]string{"a\x00b": "v"}, &Violation{Kind: KeyUnstorable, Key: "a\x00b"}},
		{"U+0000 value", map[string]string{"k": "a\x00b"}, &Violation{Kind: ValueUnstorable, Key: "k"}},
	} {
		err := Validate(test.metadata)
		if test.want == nil {
			if err != nil {
				t.Errorf("%s: Validate = %v; want nil", test.name, err)
			}
			continue
		}
		var violation *Violation
		if !errors.As(err, &violation) || !reflect.DeepEqual(violation, test.want) {
			t.Errorf("%s: Validate = %#v; want %#v", test.name, err, test.want)
		}
	}
}

func TestValidateStorableIgnoresPinnedLimits(t *testing.T) {
	if err := ValidateStorable(pairs(17)); err != nil {
		t.Fatalf("pair count applied: %v", err)
	}
	if err := ValidateStorable(map[string]string{strings.Repeat("K", 65): strings.Repeat("x", 513)}); err != nil {
		t.Fatalf("length applied: %v", err)
	}
	var violation *Violation
	if err := ValidateStorable(map[string]string{"k": "a\x00"}); !errors.As(err, &violation) || *violation != (Violation{Kind: ValueUnstorable, Key: "k"}) {
		t.Fatalf("U+0000 accepted: %v", err)
	}
}

func TestEncode(t *testing.T) {
	for _, test := range []struct {
		metadata map[string]string
		want     string
	}{
		{nil, `{}`},
		{map[string]string{}, `{}`},
		{map[string]string{"b": "2", "a": "1"}, `{"a":"1","b":"2"}`},
	} {
		got, err := Encode(test.metadata)
		if err != nil || string(got) != test.want {
			t.Errorf("Encode(%v) = %s, %v; want %s", test.metadata, got, err, test.want)
		}
	}
	// {"large":"…"} adds 12 bytes around the value.
	if _, err := Encode(map[string]string{"large": strings.Repeat("x", 64*1024-12)}); err != nil {
		t.Fatalf("boundary rejected: %v", err)
	}
	if _, err := Encode(map[string]string{"large": strings.Repeat("x", 64*1024-11)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized metadata accepted: %v", err)
	}
}
