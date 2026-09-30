package skills

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Skill is a tenant-owned Skill. Its name and description are those of its
// default version.
type Skill struct {
	ID             string
	Name           string
	Description    string
	CreatedAt      time.Time
	DefaultVersion int64
	LatestVersion  int64
}

// Version is one immutable version of a Skill. Version numbers start at 1 and
// are never reused.
type Version struct {
	ID          string
	SkillID     string
	Version     int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// Content is a version and its archive.
type Content struct {
	Version Version
	Archive []byte
}

// Page is one page of Skills; HasMore reports whether any Skill follows it.
type Page struct {
	Skills  []Skill
	HasMore bool
}

// VersionPage is one page of a Skill's versions.
type VersionPage struct {
	Versions []Version
	HasMore  bool
}

const (
	idPrefix        = "skill_"
	versionIDPrefix = "skillver_"
)

// FormatID returns the public ID of the Skill whose key is id.
func FormatID(id uuid.UUID) string { return idPrefix + id.String() }

// FormatVersionID returns the public ID of the version whose key is id.
func FormatVersionID(id uuid.UUID) string { return versionIDPrefix + id.String() }

// ParseID returns the key of a public Skill ID: "skill_" followed by a
// canonical, nonzero UUID. Any other value names no Skill and is ErrNotFound.
func ParseID(value string) (uuid.UUID, error) { return parseID(value, idPrefix) }

// ParseVersionID returns the key of a public version ID ("skillver_").
func ParseVersionID(value string) (uuid.UUID, error) { return parseID(value, versionIDPrefix) }

func parseID(value, prefix string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	if err != nil || id == uuid.Nil || value != prefix+id.String() {
		return uuid.Nil, ErrNotFound
	}
	return id, nil
}

// PathID resolves a Skill ID from a request path. A malformed value resolves
// to the maximum UUID, which Core never assigns, so the request follows
// exactly the missing-Skill path. Request-body references use ParseID.
func PathID(value string) uuid.UUID {
	id, err := ParseID(value)
	if err != nil {
		return uuid.Max
	}
	return id
}

// PathVersion resolves a version number from a request path. Versions start
// at 1, so a malformed value resolves to the never-assigned version 0 and
// follows the missing-version path. Request-body selectors use ParseVersion.
func PathVersion(value string) int64 {
	number, err := ParseVersion(value)
	if err != nil {
		return 0
	}
	return number
}

// SelectVersion resolves a Skill reference's version selector against the
// Skill's pointers: empty selects the default version, "latest" the latest,
// and any other value must be a concrete version.
func SelectVersion(selector string, defaultVersion, latestVersion int64) (int64, error) {
	switch selector {
	case "":
		return defaultVersion, nil
	case "latest":
		return latestVersion, nil
	}
	return ParseVersion(selector)
}

// ValidateSelector checks a version selector without resolving it.
func ValidateSelector(selector string) error {
	_, err := SelectVersion(selector, 1, 1)
	return err
}
