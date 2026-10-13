package projects

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
)

// APIKey is the safe metadata of a Project API key. Core stores only the
// secret's SHA-256 digest and its display prefix.
type APIKey struct {
	ID        string     `json:"id" binding:"required"`
	ProjectID string     `json:"project_id" binding:"required"`
	Name      string     `json:"name" binding:"required"`
	Prefix    string     `json:"prefix" binding:"required"`
	CreatedAt time.Time  `json:"created_at" binding:"required"`
	RevokedAt *time.Time `json:"revoked_at" extensions:"x-nullable" binding:"required"`
}

// IssuedAPIKey carries the secret once, in the response that issues it.
type IssuedAPIKey struct {
	APIKey
	Key string `json:"key" binding:"required"`
}

// KeyBinding is an active key with the principal it authenticates as.
type KeyBinding struct {
	Key       APIKey
	Principal identity.Principal
}

// KeyPage is one page of a Project's keys ordered by ID.
type KeyPage struct {
	Data    []APIKey `json:"data" binding:"required"`
	HasMore bool     `json:"has_more" binding:"required"`
}

// keyPrefixLength covers "pc_" and eight secret characters.
const keyPrefixLength = 11

// newSecret returns a new API key secret, "pc_" followed by 32 random bytes in
// unpadded base64url, and the SHA-256 digest that authenticates it.
func newSecret() (string, [sha256.Size]byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", [sha256.Size]byte{}, err
	}
	secret := "pc_" + base64.RawURLEncoding.EncodeToString(raw)
	return secret, sha256.Sum256([]byte(secret)), nil
}
