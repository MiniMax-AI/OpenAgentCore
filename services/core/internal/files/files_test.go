package files

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestUploadValidate(t *testing.T) {
	for name, test := range map[string]struct {
		upload Upload
		valid  bool
	}{
		"one byte":          {Upload{Filename: "a", Purpose: PurposeUserData}, true},
		"longest filename":  {Upload{Filename: strings.Repeat("a", 1024), Purpose: PurposeUserData}, true},
		"empty filename":    {Upload{Purpose: PurposeUserData}, false},
		"long filename":     {Upload{Filename: strings.Repeat("a", 1025), Purpose: PurposeUserData}, false},
		"invalid UTF-8":     {Upload{Filename: "bad\xff", Purpose: PurposeUserData}, false},
		"NUL":               {Upload{Filename: "bad\x00", Purpose: PurposeUserData}, false},
		"other purpose":     {Upload{Filename: "a", Purpose: "assistants"}, false},
		"missing purpose":   {Upload{Filename: "a"}, false},
		"multibyte at 1024": {Upload{Filename: strings.Repeat("é", 512), Purpose: PurposeUserData}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := test.upload.Validate()
			if test.valid != (err == nil) || err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() = %v, want valid %v", err, test.valid)
			}
		})
	}
}

func TestListQueryValidate(t *testing.T) {
	purpose := func(value string) *string { return &value }
	for name, test := range map[string]struct {
		query ListQuery
		valid bool
	}{
		"smallest page":         {ListQuery{Limit: 1}, true},
		"largest page":          {ListQuery{Limit: MaxPageSize}, true},
		"empty page":            {ListQuery{}, false},
		"oversized page":        {ListQuery{Limit: MaxPageSize + 1}, false},
		"any storable purpose":  {ListQuery{Limit: 1, Purpose: purpose("assistants")}, true},
		"invalid UTF-8 purpose": {ListQuery{Limit: 1, Purpose: purpose("\xff")}, false},
		"NUL purpose":           {ListQuery{Limit: 1, Purpose: purpose("user\x00data")}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := test.query.Validate()
			if test.valid != (err == nil) || err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() = %v, want valid %v", err, test.valid)
			}
		})
	}
}

func TestParseIDAcceptsOnlyAssignedIDs(t *testing.T) {
	id := uuid.New()
	if parsed, ok := ParseID(FormatID(id)); !ok || parsed != id {
		t.Fatalf("ParseID(FormatID) = %v, %v", parsed, ok)
	}
	for _, value := range []string{
		"", id.String(), "file_" + id.String(), "file-" + strings.ToUpper(id.String()),
		"file-{" + id.String() + "}", "file-" + strings.ReplaceAll(id.String(), "-", ""),
		"file-urn:uuid:" + id.String(), FormatID(uuid.Nil), "file-not-a-uuid",
	} {
		if _, ok := ParseID(value); ok {
			t.Errorf("ParseID(%q) accepted an ID Core never assigns", value)
		}
	}
}

func TestBoundedWriterRejectsOverflowWithoutWriting(t *testing.T) {
	var body strings.Builder
	w := &boundedWriter{body: &body, left: 4}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := w.Write([]byte("de")); n != 0 || !errors.Is(err, ErrTooLarge) {
		t.Fatal("overflowing write", n, err)
	}
	if n, err := w.Write([]byte("d")); n != 0 || !errors.Is(err, ErrTooLarge) {
		t.Fatal("write after a failure was not rejected", n, err)
	}
	if body.String() != "abc" {
		t.Fatalf("body = %q", body.String())
	}
}

func TestBoundedWriterKeepsBodyFailures(t *testing.T) {
	failure := errors.New("body failed")
	w := &boundedWriter{body: shortWriter{err: failure}, left: 10}
	if n, err := w.Write([]byte("abc")); n != 1 || !errors.Is(err, failure) {
		t.Fatal(n, err)
	}
	if _, err := w.Write([]byte("d")); !errors.Is(err, failure) {
		t.Fatal("body failure was not kept", err)
	}
	w = &boundedWriter{body: shortWriter{}, left: 10}
	if n, err := w.Write([]byte("abc")); n != 1 || err == nil {
		t.Fatal("short write without an error was accepted", n, err)
	}
}

// shortWriter accepts one byte of each write and returns err.
type shortWriter struct{ err error }

func (w shortWriter) Write(p []byte) (int, error) { return min(len(p), 1), w.err }
