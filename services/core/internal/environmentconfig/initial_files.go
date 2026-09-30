package environmentconfig

import (
	"path"
	"strings"
	"unicode/utf8"
)

// MaxInitialFileBytes bounds one installed initial file, including a copied source File.
const MaxInitialFileBytes = 50 << 20

// InitialFile keeps confidential input separate from ordinary Session configuration.
type InitialFile struct {
	Type   string `json:"type"`
	Path   string `json:"path"`
	FileID string `json:"file_id,omitempty"`
	Data   []byte `json:"data,omitempty"`
}

// InitialFileMetadata is the public description of an initial file. ID is set
// once a Session freezes the file.
type InitialFileMetadata struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Path      string `json:"path"`
	FileID    string `json:"file_id,omitempty"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

func ValidateInitialFiles(files []InitialFile) error {
	if len(files) > 50 {
		return ErrInvalid
	}
	total := 0
	seen := map[string]bool{}
	for _, f := range files {
		if !utf8.ValidString(f.Path) || strings.ContainsAny(f.Path, "\\\x00\r\n") || len(f.Path) > 4096 || !strings.HasPrefix(f.Path, "/workspace/") || path.Clean(f.Path) != f.Path || seen[f.Path] {
			return ErrInvalid
		}
		seen[f.Path] = true
		switch f.Type {
		case "inline":
			if f.FileID != "" || len(f.Data) > 5<<20 {
				return ErrInvalid
			}
			total += len(f.Data)
		case "file_id":
			if f.FileID == "" || len(f.Data) != 0 {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	if total > 10<<20 {
		return ErrInvalid
	}
	return nil
}

// InitialFilesMetadata describes requested files in order; only inline files
// know their size before a Session freezes them.
func InitialFilesMetadata(files []InitialFile) []InitialFileMetadata {
	result := make([]InitialFileMetadata, 0, len(files))
	for _, f := range files {
		m := InitialFileMetadata{Type: f.Type, Path: f.Path, FileID: f.FileID}
		if f.Type == "inline" {
			size := int64(len(f.Data))
			m.SizeBytes = &size
		}
		result = append(result, m)
	}
	return result
}
