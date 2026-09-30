package skills

import (
	"errors"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int64
	}{
		{"1", 1},
		{"42", 42},
		{"9223372036854775807", 9223372036854775807},
	} {
		got, err := ParseVersion(test.value)
		if err != nil || got != test.want {
			t.Errorf("ParseVersion(%q) = %d, %v; want %d", test.value, got, err, test.want)
		}
	}
	for _, value := range []string{"", "0", "-1", "+1", "01", " 1", "1 ", "1.0", "latest", "9223372036854775808"} {
		if got, err := ParseVersion(value); !errors.Is(err, ErrInvalidVersion) || got != 0 {
			t.Errorf("ParseVersion(%q) = %d, %v; want ErrInvalidVersion", value, got, err)
		}
	}
}
