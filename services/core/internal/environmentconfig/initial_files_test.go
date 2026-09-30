package environmentconfig

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateInitialFiles(t *testing.T) {
	for name, files := range map[string][]InitialFile{
		"outside workspace":   {{Type: "inline", Path: "/tmp/a"}},
		"unclean path":        {{Type: "inline", Path: "/workspace/a/../b"}},
		"backslash":           {{Type: "inline", Path: "/workspace/a\\b"}},
		"duplicate path":      {{Type: "inline", Path: "/workspace/a"}, {Type: "file_id", Path: "/workspace/a", FileID: "file_1"}},
		"inline with file ID": {{Type: "inline", Path: "/workspace/a", FileID: "file_1"}},
		"file ID without ID":  {{Type: "file_id", Path: "/workspace/a"}},
		"file ID with data":   {{Type: "file_id", Path: "/workspace/a", FileID: "file_1", Data: []byte("x")}},
		"unknown type":        {{Type: "url", Path: "/workspace/a"}},
		"inline over 5 MiB":   {{Type: "inline", Path: "/workspace/a", Data: make([]byte, 5<<20+1)}},
		"inline total 10 MiB": {{Type: "inline", Path: "/workspace/a", Data: make([]byte, 5<<20)}, {Type: "inline", Path: "/workspace/b", Data: make([]byte, 5<<20)}, {Type: "inline", Path: "/workspace/c", Data: []byte("x")}},
	} {
		if err := ValidateInitialFiles(files); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if err := ValidateInitialFiles([]InitialFile{{Type: "inline", Path: "/workspace/a", Data: []byte("x")}, {Type: "file_id", Path: "/workspace/b", FileID: "file_1"}}); err != nil {
		t.Fatalf("valid files rejected: %v", err)
	}
}

func TestInitialFilesMetadata(t *testing.T) {
	size := int64(3)
	got := InitialFilesMetadata([]InitialFile{{Type: "inline", Path: "/workspace/a", Data: []byte("abc")}, {Type: "file_id", Path: "/workspace/b", FileID: "file_1"}})
	want := []InitialFileMetadata{{Type: "inline", Path: "/workspace/a", SizeBytes: &size}, {Type: "file_id", Path: "/workspace/b", FileID: "file_1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata = %+v, want %+v", got, want)
	}
	if got := InitialFilesMetadata(nil); got == nil || len(got) != 0 {
		t.Fatalf("no files = %#v, want an empty list", got)
	}
}
