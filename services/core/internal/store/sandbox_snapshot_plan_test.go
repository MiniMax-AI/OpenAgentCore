package store

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sandboxSnapshotSQL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../db/queries/sandbox_reset.sql")
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Split(string(raw), "-- name: GetSandboxDeploymentSnapshot :one\n")[1]
	return strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(strings.Split(query, "\n-- name:")[0], "sqlc.embed(d)", "d.*")), ";")
}

func TestSandboxSnapshotFreshPlan(t *testing.T) {
	_, pool := newManagedTestStore(t)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "SET LOCAL jit=on"); err != nil {
		t.Fatal(err)
	}
	var stats int
	if err = tx.QueryRow(t.Context(), "SELECT count(*) FROM pg_stats WHERE tablename='runtime_deployment'").Scan(&stats); err != nil || stats != 0 {
		t.Fatal("fixture must exercise fresh cardinality", stats, err)
	}
	query := sandboxSnapshotSQL(t)
	var raw []byte
	if err = tx.QueryRow(t.Context(), "EXPLAIN (ANALYZE, FORMAT JSON) "+query).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan []map[string]any
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if _, jit := plan[0]["JIT"]; jit {
		t.Fatal("fresh singleton snapshot unexpectedly requires JIT", string(raw))
	}
	bounded := false
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["Subplan Name"] == "CTE deployment" {
				bounded = v["Plan Rows"] == float64(1)
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(plan[0])
	if !bounded {
		t.Fatal("planner lost singleton bound", string(raw))
	}
	t.Logf("fresh snapshot execution_ms=%v planning_ms=%v; singleton rows=1, no JIT", plan[0]["Execution Time"], plan[0]["Planning Time"])
	// A legal empty singleton table must not fabricate a deployment.
	if _, err = tx.Exec(t.Context(), "DELETE FROM runtime_deployment"); err != nil {
		t.Fatal(err)
	}
	var empty any
	if err = tx.QueryRow(t.Context(), query).Scan(&empty); err != pgx.ErrNoRows {
		t.Fatal("empty deployment produced a row", err)
	}
}

// Compare both complete projections in one MVCC statement and at one as_of,
// including every resource/rollout field. Only the cardinality annotation differs.
func assertSandboxSnapshotEquivalent(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	query := sandboxSnapshotSQL(t)
	legacy := strings.Replace(query, "WITH deployment AS MATERIALIZED (SELECT * FROM runtime_deployment WHERE singleton = true LIMIT 1),\nobserved AS", "WITH observed AS", 1)
	legacy = strings.ReplaceAll(legacy, "CROSS JOIN deployment d", "CROSS JOIN runtime_deployment d")
	legacy = strings.TrimSuffix(legacy, "\nWHERE d.singleton = true\nLIMIT 1")
	query = strings.Replace(query, "clock_timestamp() AS as_of", "(SELECT as_of FROM snapshot_time) AS as_of", 1)
	legacy = strings.Replace(legacy, "clock_timestamp() AS as_of", "(SELECT as_of FROM snapshot_time) AS as_of", 1)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	// Avoid paying the legacy planner's JIT cost in this result-equivalence check.
	// The separate fresh-plan test keeps JIT enabled.
	if _, err = tx.Exec(t.Context(), "SET LOCAL jit=off"); err != nil {
		t.Fatal(err)
	}
	var same bool
	err = tx.QueryRow(t.Context(), "WITH snapshot_time AS MATERIALIZED (SELECT clock_timestamp() AS as_of) SELECT (SELECT to_jsonb(s) FROM ("+query+") s) IS NOT DISTINCT FROM (SELECT to_jsonb(s) FROM ("+legacy+") s)").Scan(&same)
	if err != nil || !same {
		t.Fatal("singleton annotation changed snapshot", same, err)
	}
}
