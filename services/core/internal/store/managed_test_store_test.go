package store

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
)

// Deployment identity belongs to a whole database, so managed fixtures cannot
// share the ordinary Store fixture database or bypass production startup checks.
func newManagedTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenIsolated(t, nil)
	// Hosted Sessions freeze a model provider, which needs a credential key.
	return NewWithCredentialCipher(pool, fixtureCipher), pool
}

func NewManagedTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	return newManagedTestStore(t)
}
