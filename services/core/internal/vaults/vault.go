package vaults

import (
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Vault is a tenant-owned resource, independent of Sessions and engine execution.
type Vault struct {
	ID        string
	TenantID  string
	Name      *string
	Metadata  map[string]string
	CreatedAt time.Time
}

type VaultPage struct {
	Vaults     []Vault
	NextCursor string
}

// validName checks a Vault or Credential name the public layer has already
// trimmed.
func validName(name string) bool {
	return len(name) >= 1 && len(name) <= 256 && utf8.ValidString(name)
}

// canonicalID returns the canonical form of a nonzero UUID.
func canonicalID(value string) (string, bool) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return "", false
	}
	return id.String(), true
}
