package files

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// MaxBytes bounds the content of one File.
	MaxBytes int64 = 512 << 20
	// MaxPageSize bounds one List page.
	MaxPageSize = 10000
	// PurposeUserData is the only purpose Core stores.
	PurposeUserData = "user_data"

	idPrefix = "file-"
)

// File is a stored File's immutable metadata.
type File struct {
	ID        string
	Filename  string
	Purpose   string
	SizeBytes int64
	CreatedAt time.Time
}

// Upload is the envelope read alongside an upload's body.
type Upload struct {
	Filename string
	Purpose  string
}

// Validate accepts the envelopes Core stores: a filename of 1 to 1024 bytes of
// valid UTF-8 without U+0000, and the user_data purpose.
func (u Upload) Validate() error {
	if len(u.Filename) < 1 || len(u.Filename) > 1024 || !validText(u.Filename) || u.Purpose != PurposeUserData {
		return ErrInvalidInput
	}
	return nil
}

// ListQuery selects one page of a tenant's Files, ordered by creation time and
// then ID.
type ListQuery struct {
	// After is the ID of the previous page's last File, or empty for the first
	// page.
	After     string
	Limit     int
	Ascending bool
	// Purpose, when not nil, keeps only Files with this purpose.
	Purpose *string
}

// Validate accepts a page size of 1 to MaxPageSize and a purpose filter that
// PostgreSQL can compare.
func (q ListQuery) Validate() error {
	if q.Limit < 1 || q.Limit > MaxPageSize {
		return fmt.Errorf("%w: page size must be 1..%d", ErrInvalidInput, MaxPageSize)
	}
	if q.Purpose != nil && !validText(*q.Purpose) {
		return ErrInvalidInput
	}
	return nil
}

// Page is one List page. NextCursor is the ID of its last File when more Files
// follow, and empty otherwise.
type Page struct {
	Files      []File
	NextCursor string
}

// FormatID returns the ID of the File stored under id.
func FormatID(id uuid.UUID) string { return idPrefix + id.String() }

// ParseID returns the stored UUID that a File ID names. ok is false for an ID
// Core never assigns: another spelling, another prefix or the nil UUID.
func ParseID(id string) (uuid.UUID, bool) {
	parsed, err := uuid.Parse(strings.TrimPrefix(id, idPrefix))
	if err != nil || parsed == uuid.Nil || id != FormatID(parsed) {
		return uuid.UUID{}, false
	}
	return parsed, true
}

func validText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}
