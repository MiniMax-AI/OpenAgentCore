package migrations_test

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/migrations"
)

func TestApplyRefusesANewerSchema(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	if _, err := pool.Exec(t.Context(), "INSERT INTO agents_api_schema_version (version_id, is_applied) VALUES (999999, true)"); err != nil {
		t.Fatal(err)
	}
	connection := stdlib.RegisterConnConfig(pool.Config().ConnConfig)
	defer stdlib.UnregisterConnConfig(connection)
	err := migrations.Apply(t.Context(), connection)
	if err == nil || !strings.HasPrefix(err.Error(), "Agents API database schema version 999999 is newer than this Core supports (") {
		t.Fatalf("err = %v", err)
	}
}
