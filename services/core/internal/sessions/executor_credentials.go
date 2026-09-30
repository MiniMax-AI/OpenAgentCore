package sessions

import (
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
)

type IssuedExecutorCredential struct {
	KeyID         string `json:"key_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Token         string `json:"executor_token"`
}

// ExecutorCredential is the metadata of one Environment executor credential.
// Its secret is returned only when issued or rotated.
type ExecutorCredential struct {
	KeyID     string     `json:"key_id" format:"uuid"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at" extensions:"x-nullable"`
}

// ExecutorConnectionState is an internal durable observation, never a wire payload.
// In particular the current credential digest must not be serialized.
type ExecutorConnectionState struct {
	DeviceID          string     `json:"-"`
	BoundKeyID        *string    `json:"-"`
	EnrolledAt        *time.Time `json:"-"`
	LastSeenAt        *time.Time `json:"-"`
	CredentialHash    string     `json:"-"`
	EnvironmentStatus string     `json:"-"`
}

type ExecutorCredentialState struct {
	EnvironmentID string `json:"-"`
	Credentials   []ExecutorCredential
	Connection    ExecutorConnectionState
}

// InstallationAuthorization permits claiming one Environment's connect-only key.
// The Environment UUID is reserved as that key's ID. Reissuing an authorization
// never rotates or revives the key, and a retry must prove the same local secret.
type InstallationAuthorization struct {
	Principal   identity.Principal `json:"principal"`
	Environment string             `json:"environment_id"`
	Version     string             `json:"version"`
	ExpiresAt   int64              `json:"expires_at"`
}
