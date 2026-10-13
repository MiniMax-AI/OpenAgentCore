package integration

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
	return New(t, pool), pool
}

// configuredStore is newManagedTestStore claimed for a new installation and
// configured for E2B, as Web setup leaves an installation, so that it admits
// hosted work. It returns the installation.
func configuredStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, _ := newManagedTestStore(t)
	return s, webDeployment(t, s, "e2b")
}

// reopenStore is the fixture on a new pool to s's database, as after a restart.
// The pool closes when the test ends.
func reopenStore(t *testing.T, s *Store) *Store {
	t.Helper()
	pool, err := pgxpool.NewWithConfig(t.Context(), s.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(t, pool)
}
