package jsonobject

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{``, `{}`},
		{` { } `, `{}`},
		{` {"environment":{"type":"none"},"agent":{"revision":9007199254740993,"model":"example"}} `, `{"agent":{"model":"example","revision":9007199254740993},"environment":{"type":"none"}}`},
		{`{"value":1.50e3}`, `{"value":1.50e3}`},
	} {
		got, err := Normalize([]byte(test.input))
		if err != nil || string(got) != test.want {
			t.Errorf("Normalize(%q) = %s, %v; want %s", test.input, got, err, test.want)
		}
	}
	for _, raw := range []string{`null`, `[]`, `"text"`, `1`, `{} {}`, `{} null`, `{`} {
		if _, err := Normalize([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Errorf("Normalize(%q) = %v; want ErrInvalid", raw, err)
		}
	}
}
