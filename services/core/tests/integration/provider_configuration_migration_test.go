package integration

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/google/uuid"
)

func TestProviderConfigurationMigrationPreservesCiphertextAndRetainedOwnership(t *testing.T) {
	db, migration := runtimeNamesMigrationSchema(t)
	ctx := t.Context()
	if _, err := migration.UpTo(ctx, 90); err != nil {
		t.Fatal(err)
	}
	installation := uuid.NewString()
	cipher := pgtest.CredentialKey(t)
	secret := []byte("migration-provider-key")
	before, err := cipher.SealSandboxDeployment(secret, installation, 2)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The current deployment and retained generation exist in the old schema.
	// A partial observation is valid and must retain its null fields.
	exec(`UPDATE runtime_deployment SET installation_id=$1,backend_fingerprint=repeat('a',64),provider_kind='e2b',mode='direct',web_managed=true,generation=2,
  e2b_template='next-template',e2b_credential=$2,e2b_api_url='https://api.e2b.app',e2b_domain='e2b.app',e2b_template_cpus=2`, installation, before)
	exec(`INSERT INTO runtime_deployment_generations(generation,provider_kind,specification,e2b_template,e2b_api_url,e2b_domain)
  VALUES(1,'e2b','{}','old-template','https://api.e2b.app','e2b.app')`)
	session, tenant, environment, device, allocation := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration) VALUES($1,$2,'codex','old','old','{}')`, session, tenant)
	exec(`INSERT INTO environments(id,session_id) VALUES($1,$2)`, environment, session)
	exec(`INSERT INTO devices(id,tenant_id,name,credential_hash) VALUES($1,$2,'old',repeat('b',64))`, device, tenant)
	exec(`INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,deployment_generation) VALUES($1,$2,$3,$4,1)`, allocation, environment, device, installation)
	var allocationBefore, allocationAfter string
	if err := db.QueryRowContext(ctx, `SELECT to_jsonb(a)::text FROM runtime_allocations a WHERE id=$1`, allocation).Scan(&allocationBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.UpTo(ctx, 91); err != nil {
		t.Fatal(err)
	}
	var after, metadataJSON []byte
	var restoredInstallation, template string
	var generation uint64
	if err := db.QueryRowContext(ctx, `SELECT provider_credential,installation_id::text,generation,provider_config->>'template',provider_metadata FROM runtime_deployment`).Scan(&after, &restoredInstallation, &generation, &template, &metadataJSON); err != nil || !bytes.Equal(before, after) {
		t.Fatal("ciphertext rewritten", err)
	}
	restored, err := cipher.OpenSandboxDeployment(after, restoredInstallation, generation)
	if err != nil || !bytes.Equal(restored, secret) || restoredInstallation != installation || generation != 2 || template != "next-template" {
		t.Fatal("ownership or credential changed", err)
	}
	var metadata struct {
		TemplateBuild struct {
			Resources struct {
				CPUs   *int `json:"cpus"`
				Memory *int `json:"memory_mib"`
			} `json:"resources"`
		} `json:"template_build"`
	}
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil || metadata.TemplateBuild.Resources.CPUs == nil || *metadata.TemplateBuild.Resources.CPUs != 2 || metadata.TemplateBuild.Resources.Memory != nil {
		t.Fatal("partial metadata lost", string(metadataJSON), err)
	}
	var retained int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM runtime_deployment_generations WHERE generation=1 AND provider_config->>'template'='old-template'`).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("retained ownership lost", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment_generations SET provider_config='{}'`); err == nil {
		t.Fatal("migration removed immutability")
	}
	if _, err := migration.DownTo(ctx, 90); err != nil {
		t.Fatal(err)
	}
	var cpu int
	var memory *int
	if err := db.QueryRowContext(ctx, `SELECT e2b_credential,e2b_template_cpus,e2b_template_memory_mib FROM runtime_deployment`).Scan(&after, &cpu, &memory); err != nil || !bytes.Equal(before, after) || cpu != 2 || memory != nil {
		t.Fatal("downgrade lost valid data", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT to_jsonb(a)::text FROM runtime_allocations a WHERE id=$1`, allocation).Scan(&allocationAfter); err != nil || allocationAfter != allocationBefore {
		t.Fatal("migration changed retained allocation", err)
	}
}
