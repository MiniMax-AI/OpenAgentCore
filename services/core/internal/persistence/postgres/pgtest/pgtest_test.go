package pgtest

import "testing"

func TestDatabaseGuardUsesEffectiveDatabase(t *testing.T) {
	for _, dsn := range []string{
		"postgres://localhost/oac_local_tests?dbname=oac",
		"host=localhost dbname=oac",
		"postgres://localhost/oac",
	} {
		if _, err := databaseConfig(dsn); err == nil {
			t.Fatalf("unsafe database accepted: %s", dsn)
		}
	}
	cfg, err := databaseConfig("postgres://localhost/oac_local_tests")
	if err != nil || cfg.ConnConfig.Database != "oac_local_tests" {
		t.Fatalf("valid dedicated database rejected: %v", err)
	}
}
