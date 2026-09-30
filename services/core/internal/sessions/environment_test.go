package sessions

import (
	"errors"
	"testing"
)

func TestCreatesEnvironment(t *testing.T) {
	for configuration, want := range map[string]bool{
		`{}`:                                       false,
		`{"environment":null}`:                     false,
		`{"environment":{"type":"none"}}`:          false,
		`{"environment":{"type":"self_hosted"}}`:   true,
		`{"environment":{"type":"openai_hosted"}}`: true,
	} {
		if got, err := CreatesEnvironment([]byte(configuration)); err != nil || got != want {
			t.Fatalf("%s: %v, %v", configuration, got, err)
		}
	}
	for _, configuration := range []string{`not json`, `{"environment":{"type":"docker"}}`, `{"environment":{}}`} {
		if _, err := CreatesEnvironment([]byte(configuration)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: %v", configuration, err)
		}
	}
}
