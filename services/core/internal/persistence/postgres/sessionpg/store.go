package sessionpg

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Store is the pooled Session adapter: the storage of the Session use cases
// and the Session reads. cipher opens the Session data frozen at creation; it
// is nil on a service without a credential key.
type Store struct {
	units  *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

// New builds the pooled Session adapter on units, opening frozen Session data
// with cipher.
func New(units *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{units: units, cipher: cipher}
}

var (
	_ sessions.Storage = (*Store)(nil)
	_ sessions.Reader  = (*Store)(nil)
)
