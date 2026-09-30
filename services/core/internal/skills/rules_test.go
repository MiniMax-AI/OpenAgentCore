package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseID(t *testing.T) {
	id := uuid.New()
	for _, test := range []struct {
		name, value string
		parse       func(string) (uuid.UUID, error)
		want        uuid.UUID
	}{
		{"skill", FormatID(id), ParseID, id},
		{"version", FormatVersionID(id), ParseVersionID, id},
		{"bare UUID", id.String(), ParseID, uuid.Nil},
		{"version prefix for a Skill", FormatVersionID(id), ParseID, uuid.Nil},
		{"Skill prefix for a version", FormatID(id), ParseVersionID, uuid.Nil},
		{"upper case", "skill_" + strings.ToUpper(id.String()), ParseID, uuid.Nil},
		{"braced", "skill_{" + id.String() + "}", ParseID, uuid.Nil},
		{"nil UUID", FormatID(uuid.Nil), ParseID, uuid.Nil},
		{"empty", "", ParseID, uuid.Nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.parse(test.value)
			if test.want == uuid.Nil {
				if !errors.Is(err, ErrNotFound) || got != uuid.Nil {
					t.Fatalf("got %s, %v", got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("got %s, %v", got, err)
			}
		})
	}
}

// Malformed path segments resolve to values Core never assigns, so they take
// the missing-resource path.
func TestPathValues(t *testing.T) {
	id := uuid.New()
	for value, want := range map[string]uuid.UUID{FormatID(id): id, "skill_missing": uuid.Max, id.String(): uuid.Max, "": uuid.Max} {
		if got := PathID(value); got != want {
			t.Errorf("PathID(%q) = %s", value, got)
		}
	}
	for value, want := range map[string]int64{"1": 1, "42": 42, "0": 0, "01": 0, "-1": 0, "latest": 0, "": 0, "9223372036854775808": 0} {
		if got := PathVersion(value); got != want {
			t.Errorf("PathVersion(%q) = %d", value, got)
		}
	}
}

func TestSelectVersion(t *testing.T) {
	for _, test := range []struct {
		selector string
		want     int64
		valid    bool
	}{
		{"", 2, true},
		{"latest", 5, true},
		{"3", 3, true},
		{"0", 0, false},
		{"03", 0, false},
		{"Latest", 0, false},
		{"default", 0, false},
	} {
		got, err := SelectVersion(test.selector, 2, 5)
		if test.valid != (err == nil) || got != test.want {
			t.Errorf("SelectVersion(%q) = %d, %v", test.selector, got, err)
		}
		if test.valid != (ValidateSelector(test.selector) == nil) {
			t.Errorf("ValidateSelector(%q) disagrees", test.selector)
		}
	}
}

func TestDecideVersionDeletion(t *testing.T) {
	skill := Skill{DefaultVersion: 2, LatestVersion: 4}
	for _, test := range []struct {
		name   string
		target int64
		others bool
		want   VersionDeletion
		err    error
	}{
		{"default with others", 2, true, VersionDeletion{}, ErrDefaultVersion},
		{"sole default", 2, false, VersionDeletion{Target: Version{Version: 2}, DeleteSkill: true}, nil},
		{"latest", 4, true, VersionDeletion{Target: Version{Version: 4}, RefreshLatest: true}, nil},
		{"middle", 3, true, VersionDeletion{Target: Version{Version: 3}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecideVersionDeletion(VersionDeletionFacts{Skill: skill, Target: Version{Version: test.target}, OthersRemain: test.others})
			if !errors.Is(err, test.err) || got != test.want {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestCursorPrefixError(t *testing.T) {
	var cursor *CursorError
	if err := cursorPrefixError("skill_x"); !errors.As(err, &cursor) || cursor.Message != "Invalid 'after': 'skill_x'. Expected an ID that begins with 'skillver'." {
		t.Fatalf("echoed: %v", err)
	}
	if err := cursorPrefixError(strings.Repeat("x", 1000)); !errors.As(err, &cursor) || cursor.Message != "Invalid 'after'. Expected an ID that begins with 'skillver'." {
		t.Fatalf("long value: %v", err)
	}
	if err := cursorPrefixError("bad\x00"); !errors.As(err, &cursor) || cursor.Message != "Invalid 'after'. Expected an ID that begins with 'skillver'." {
		t.Fatalf("unprintable value: %v", err)
	}
}

func TestVerifyContent(t *testing.T) {
	archive := testArchive(t, "proof", "Verify a Skill.")
	version := Version{Name: "proof", Description: "Verify a Skill."}
	if err := VerifyContent(Content{Version: version, Archive: archive}); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]Content{
		"other name":        {Version: Version{Name: "other", Description: version.Description}, Archive: archive},
		"other description": {Version: Version{Name: version.Name, Description: "Other."}, Archive: archive},
		"corrupt archive":   {Version: version, Archive: archive[:len(archive)/2]},
	} {
		if err := VerifyContent(content); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func testArchive(t *testing.T, name, description string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: name + "/SKILL.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("---\nname: " + name + "\ndescription: " + description + "\n---\nProof.")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
