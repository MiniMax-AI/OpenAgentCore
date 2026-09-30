package store

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewWithCredentialCipher configures immutable credential encryption before the
// Store is published. A nil cipher leaves non-secret resource operations available.
func NewWithCredentialCipher(pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) *Store {
	s := New(pool)
	s.credentialCipher = cipher
	return s
}
