package pgunit

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIdentifierParsing(t *testing.T) {
	valid := uuid.NewString()
	unknown := uuid.Max.String()
	for _, tc := range []struct {
		value  string
		parsed bool
		path   string
		cursor string
	}{
		{valid, true, valid, valid},
		{strings.ToUpper(valid), true, valid, strings.ToUpper(valid)},
		{"", false, unknown, unknown},
		{"not-a-uuid", false, unknown, unknown},
		{uuid.Nil.String(), false, unknown, unknown},
	} {
		id, err := ParseID(tc.value)
		if tc.parsed != (err == nil) || !tc.parsed && !errors.Is(err, ErrInvalidID) || tc.parsed && uuid.UUID(id.Bytes).String() != tc.path {
			t.Errorf("ParseID(%q) = %v, %v", tc.value, id, err)
		}
		if path := PathID(tc.value); !path.Valid || uuid.UUID(path.Bytes).String() != tc.path {
			t.Errorf("PathID(%q) = %v", tc.value, path)
		}
		if cursor := LookupCursor(tc.value); cursor != tc.cursor {
			t.Errorf("LookupCursor(%q) = %q", tc.value, cursor)
		}
	}
}
