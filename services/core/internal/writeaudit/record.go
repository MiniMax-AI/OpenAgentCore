package writeaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ErrInvalidSource reports a write audit record that cannot be recorded:
// malformed provenance, or an action or resource outside the audit
// vocabulary. The write it belongs to fails closed.
var ErrInvalidSource = errors.New("invalid write audit source")

// Resource identifies a resource genuinely created by the current transaction.
type Resource struct{ Type, ID, ParentID string }

// ValidResourceType reports whether value is an audited resource type. The list
// is closed; recording and owner queries share it.
func ValidResourceType(value string) bool {
	switch value {
	case "agent", "session", "environment", "environment_template", "skill", "skill_version", "file", "vault", "credential", "artifact":
		return true
	}
	return false
}

// ValidText reports whether value is valid UTF-8 of at most max runes without
// control characters, and nonempty when required. Every recorded or queried
// audit string passes it.
func ValidText(value string, max int, required bool) bool {
	return (!required || value != "") && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

// ValidKeyDigest reports whether digest is the lowercase hexadecimal SHA-256
// digest that identifies a Project key.
func ValidKeyDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == digest
}

// Validate checks that s is well-formed provenance of a write in tenant.
func (s Source) Validate(tenant string) error {
	if !s.valid(tenant) {
		return fmt.Errorf("%w: invalid provenance", ErrInvalidSource)
	}
	return nil
}

// ValidateRecord checks a write record in tenant: source must be well-formed
// provenance from tenant, action an audited write action, and every resource
// an audited resource.
func ValidateRecord(source Source, tenant, action string, resources []Resource) error {
	if err := source.Validate(tenant); err != nil {
		return err
	}
	switch action {
	case "create", "update", "delete", "send_events", "upload_file", "upload_version", "update_default_version":
	default:
		return fmt.Errorf("%w: invalid action", ErrInvalidSource)
	}
	for _, resource := range resources {
		if !ValidResourceType(resource.Type) || !ValidText(resource.ID, 256, true) || !ValidText(resource.ParentID, 256, false) {
			return fmt.Errorf("%w: invalid resource", ErrInvalidSource)
		}
	}
	return nil
}

func (s Source) valid(tenant string) bool {
	actual, err := parseID(s.TenantID)
	expected, expectedErr := parseID(tenant)
	valid := err == nil && expectedErr == nil && actual == expected &&
		ValidText(s.Name, 80, false) && ValidText(s.RequestID, 128, true) && ValidText(s.TraceID, 128, true)
	switch s.Kind {
	case "static", "console":
		digest := strings.TrimPrefix(s.KeyID, "static:")
		return valid && strings.HasPrefix(s.KeyID, "static:") && ValidKeyDigest(digest) && s.Prefix == digest[:min(len(digest), 8)]
	case "issued":
		_, idErr := parseID(s.KeyID)
		valid = valid && idErr == nil && len(s.Prefix) == 11 && strings.HasPrefix(s.Prefix, "pc_")
		for _, c := range strings.TrimPrefix(s.Prefix, "pc_") {
			valid = valid && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-')
		}
		return valid
	}
	return false
}

func parseID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err == nil && id == uuid.Nil {
		err = errors.New("nil UUID")
	}
	return id, err
}
