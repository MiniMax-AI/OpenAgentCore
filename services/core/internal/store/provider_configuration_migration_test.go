package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"testing"
)

func TestProviderConfigurationMigrationPreservesCiphertextAndRetainedOwnership(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	archiveAllocation(t, w, tenant, session, view.InstallationID)
	input.Configuration.(*e2b.DeploymentConfiguration).Template = "next:" + uuid.NewString()
	if _, err := w.UpdateSandboxDeployment(SandboxResetTestContext(t.Context()), view.InstallationID, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(stdlib.GetConnector(*s.pool.Config().ConnConfig))
	defer db.Close()
	migration, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migration.DownTo(t.Context(), 90); err != nil {
		t.Fatal(err)
	}
	// A valid old record can have only part of its observation populated.
	if _, err = db.ExecContext(t.Context(), "UPDATE runtime_deployment SET e2b_template_cpus=2"); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	var generation int64
	if err = db.QueryRowContext(t.Context(), "SELECT e2b_credential,generation FROM runtime_deployment").Scan(&before, &generation); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(t.Context(), "SELECT provider_credential FROM runtime_deployment").Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("ciphertext rewritten", err)
	}
	restored, err := s.GetSandboxSetup(t.Context())
	if err != nil || restored.Generation != uint64(generation) || restored.Configuration.(*e2b.DeploymentConfiguration).APIKey != input.Configuration.(*e2b.DeploymentConfiguration).APIKey {
		t.Fatal("ownership or credential changed", err)
	}
	public, err := s.GetRuntimeDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		TemplateBuild struct {
			Resources struct {
				CPUs   *int `json:"cpus"`
				Memory *int `json:"memory_mib"`
			} `json:"resources"`
		} `json:"template_build"`
	}
	if err = json.Unmarshal(public.Metadata, &metadata); err != nil || metadata.TemplateBuild.Resources.CPUs == nil || *metadata.TemplateBuild.Resources.CPUs != 2 || metadata.TemplateBuild.Resources.Memory != nil {
		t.Fatal("partial metadata lost", string(public.Metadata), err)
	}
	var retained int
	if err = db.QueryRowContext(t.Context(), "SELECT count(*) FROM runtime_deployment_generations WHERE generation=1 AND provider_config->>'template'<>$1", input.Configuration.(*e2b.DeploymentConfiguration).Template).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("retained ownership lost", err)
	}
	if _, err = db.ExecContext(t.Context(), "UPDATE runtime_deployment_generations SET provider_config='{}'"); err == nil {
		t.Fatal("migration removed immutability")
	}
	if _, err = migration.DownTo(t.Context(), 90); err != nil {
		t.Fatal(err)
	}
	var cpu int
	var memory *int
	if err = db.QueryRowContext(t.Context(), "SELECT e2b_credential,e2b_template_cpus,e2b_template_memory_mib FROM runtime_deployment").Scan(&after, &cpu, &memory); err != nil || !bytes.Equal(before, after) || cpu != 2 || memory != nil {
		t.Fatal("downgrade lost valid data", err)
	}
}
