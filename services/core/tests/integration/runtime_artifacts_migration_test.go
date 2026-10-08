package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRuntimeArtifactsMigrationProtectsRetainedSpecifications(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		for _, location := range []string{"current", "history", "both"} {
			t.Run(direction+"/"+location, func(t *testing.T) {
				db, migration := runtimeNamesMigrationSchema(t)
				ctx := t.Context()
				version := int64(98)
				if direction == "down" {
					version = 99
				}
				if _, err := migration.UpTo(ctx, version); err != nil {
					t.Fatal(err)
				}
				exec := func(query string, args ...any) {
					t.Helper()
					if _, err := db.ExecContext(ctx, query, args...); err != nil {
						t.Fatal(err)
					}
				}
				hash := strings.Repeat("a", 64)
				runtime := `{"source_commit":"` + strings.Repeat("b", 40) + `","image_id":"sha256:` + hash + `","image_manifest_digest":"sha256:` + hash + `","microsandbox_ref":"oac-runtime@sha256:` + hash + `","runtime_sha256":"` + hash + `","firmware_sha256":"` + hash + `"}`
				if direction == "down" {
					runtime = `{"source_commit":"` + strings.Repeat("b", 40) + `","artifacts":{"image_id":"sha256:` + hash + `","image_manifest_digest":"sha256:` + hash + `"}}`
				}
				specification := `{"resources":{"cpus":2,"memory_mib":2048},"runtime":` + runtime + `}`
				// An E2B current selection must not hide an incompatible historical pin.
				exec(`UPDATE runtime_deployment SET provider_kind='e2b',mode='direct',generation=2,specification='{"resources":{"cpus":2,"memory_mib":2048}}'`)
				if location != "history" {
					exec(`UPDATE runtime_deployment SET provider_kind='docker',mode='nodes',specification=$1`, specification)
				}
				if location != "current" {
					exec(`INSERT INTO runtime_deployment_generations(generation,provider_kind,specification) VALUES(1,'docker',$1)`, specification)
				}
				digest := sha256.Sum256([]byte(`{"provider":"docker",` + specification[1:]))
				node, connection := uuid.NewString(), uuid.NewString()
				exec(`INSERT INTO runtime_nodes(id,installation_id,name,backend_fingerprint,credential_sha256,max_active,max_retained,connection_id,connected_epoch,deployment_generation,ready_generation,specification_digest)
					VALUES($1,$2,'retained', $3,$3,1,2,$4,1,1,1,$5)`, node, uuid.NewString(), hash, connection, hex.EncodeToString(digest[:]))
				exec(`INSERT INTO runtime_node_generation_status(node_id,generation,specification_digest,connection_id,owner_epoch,state) VALUES($1,1,$2,$3,1,'ready')`, node, hex.EncodeToString(digest[:]), connection)
				snapshot := func(table string) string {
					t.Helper()
					var value string
					if err := db.QueryRowContext(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text), '[]'::jsonb)::text FROM "+table+" r").Scan(&value); err != nil {
						t.Fatal(err)
					}
					return value
				}
				before := map[string]string{}
				for _, table := range []string{"runtime_deployment", "runtime_deployment_generations", "runtime_nodes", "runtime_node_generation_status", "agents_api_schema_version"} {
					before[table] = snapshot(table)
				}
				var err error
				if direction == "up" {
					_, err = migration.UpTo(ctx, 99)
				} else {
					_, err = migration.DownTo(ctx, 98)
				}
				if err == nil || !strings.Contains(err.Error(), "installation-version-policy") {
					t.Fatalf("migration must reject incompatible retained specifications: %v", err)
				}
				for table, original := range before {
					if snapshot(table) != original {
						t.Fatalf("refused migration changed %s", table)
					}
				}
			})
		}
	}
}

func TestRuntimeArtifactsMigrationAllowsEmptyAndE2BInstallations(t *testing.T) {
	for _, scenario := range []string{"empty", "e2b", "e2b_history"} {
		t.Run(scenario, func(t *testing.T) {
			db, migration := runtimeNamesMigrationSchema(t)
			ctx := t.Context()
			if _, err := migration.UpTo(ctx, 98); err != nil {
				t.Fatal(err)
			}
			if scenario != "empty" {
				if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment SET provider_kind='e2b',mode='direct',generation=2,specification='{"resources":{"cpus":2,"memory_mib":2048}}'`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "e2b_history" {
				if _, err := db.ExecContext(ctx, `INSERT INTO runtime_deployment_generations(generation,provider_kind,specification) VALUES(1,'e2b','{"resources":{"cpus":1,"memory_mib":1024}}')`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := migration.UpTo(ctx, 99); err != nil {
				t.Fatal(err)
			}
			if _, err := migration.DownTo(ctx, 98); err != nil {
				t.Fatal(err)
			}
		})
	}
}
