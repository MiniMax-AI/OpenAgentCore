package store

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestProviderRegistrationDowngradePreservesCustomEndpoints(t *testing.T) {
	s, w, view, input := webSpecificationFixture(t, "e2b")
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	archiveAllocation(t, w, tenant, session, view.InstallationID)
	input.Configuration.(*e2b.DeploymentConfiguration).APIURL, input.Configuration.(*e2b.DeploymentConfiguration).Domain = "https://api.example.test", "example.test"
	input.ExpectedGeneration = view.Generation
	if _, err := deploymentExecution(t, w).Update(SandboxResetTestContext(t.Context()), view.InstallationID, input); err != nil {
		t.Fatal(err)
	}
	tenant, session = managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	archiveAllocation(t, w, tenant, session, view.InstallationID)
	input.Configuration.(*e2b.DeploymentConfiguration).Template = "next:" + uuid.NewString()
	input.ExpectedGeneration = 2
	if _, err := deploymentExecution(t, w).Update(SandboxResetTestContext(t.Context()), view.InstallationID, input); err != nil {
		t.Fatal(err)
	}
	// Exercise the historical migration with the target schema's disabled
	// suspension policy, independently of the current provider defaults.
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_deployment SET idle_seconds=0,retention_seconds=0"); err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(stdlib.GetConnector(*s.pool.Config().ConnConfig))
	defer db.Close()
	migrations, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.DownTo(t.Context(), 88); err != nil {
		t.Fatal(err)
	}
	for generation, want := range map[int]string{1: "", 2: input.Configuration.(*e2b.DeploymentConfiguration).APIURL} {
		var endpoint string
		if err := db.QueryRowContext(t.Context(), "SELECT e2b_api_url FROM runtime_deployment_generations WHERE generation=$1", generation).Scan(&endpoint); err != nil || endpoint != want {
			t.Fatal("downgrade changed endpoint identity", generation, endpoint, err)
		}
	}
	var endpoint string
	if err := db.QueryRowContext(t.Context(), "SELECT e2b_api_url FROM runtime_deployment").Scan(&endpoint); err != nil || endpoint != input.Configuration.(*e2b.DeploymentConfiguration).APIURL {
		t.Fatal("downgrade changed current endpoint", endpoint, err)
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE runtime_deployment_generations SET e2b_domain='changed.test' WHERE generation=1"); err == nil || !strings.Contains(err.Error(), "Retained sandbox specifications are immutable") {
		t.Fatal("downgrade left specifications mutable", err)
	}
	if _, err := migrations.DownTo(t.Context(), 86); err == nil || !strings.Contains(err.Error(), "custom endpoint is retained") {
		t.Fatal("downgrade discarded a custom endpoint", err)
	}
}
