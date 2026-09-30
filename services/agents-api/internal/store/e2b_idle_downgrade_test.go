package store

import (
	"database/sql"
	"testing"
)

// These tests exercise older migration guards. Their historical E2B schema
// accepts only the zero idle policy, so opt the isolated fixture out first.
func disableE2BIdlePolicyForLegacyDowngrade(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `UPDATE runtime_deployment SET idle_seconds=0, retention_seconds=0 WHERE provider_kind='e2b'`); err != nil {
		t.Fatal(err)
	}
}
