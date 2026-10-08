package integration

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"

	"github.com/google/uuid"
)

func TestProviderRegistrationDowngradePreservesCustomEndpoints(t *testing.T) {
	db, migrations := runtimeNamesMigrationSchema(t)
	ctx := t.Context()
	if _, err := migrations.UpTo(ctx, 89); err != nil {
		t.Fatal(err)
	}
	installation := uuid.NewString()
	credential, err := pgtest.CredentialKey(t).SealSandboxDeployment([]byte("migration-provider-key"), installation, 3)
	if err != nil {
		t.Fatal(err)
	}
	const customEndpoint = "https://api.example.test"
	if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment SET installation_id=$1,backend_fingerprint=repeat('a',64),provider_kind='e2b',mode='direct',web_managed=true,generation=3,
  e2b_template='next-template',e2b_credential=$2,e2b_api_url=$3,e2b_domain='example.test'`, installation, credential, customEndpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO runtime_deployment_generations(generation,provider_kind,specification,e2b_template,e2b_api_url,e2b_domain) VALUES
  (1,'e2b','{}','old-template','https://api.e2b.app','e2b.app'),(2,'e2b','{}','old-template',$1,'example.test')`, customEndpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.DownTo(t.Context(), 88); err != nil {
		t.Fatal(err)
	}
	for generation, want := range map[int]string{1: "", 2: customEndpoint} {
		var endpoint string
		if err := db.QueryRowContext(t.Context(), "SELECT e2b_api_url FROM runtime_deployment_generations WHERE generation=$1", generation).Scan(&endpoint); err != nil || endpoint != want {
			t.Fatal("downgrade changed endpoint identity", generation, endpoint, err)
		}
	}
	var endpoint string
	if err := db.QueryRowContext(t.Context(), "SELECT e2b_api_url FROM runtime_deployment").Scan(&endpoint); err != nil || endpoint != customEndpoint {
		t.Fatal("downgrade changed current endpoint", endpoint, err)
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE runtime_deployment_generations SET e2b_domain='changed.test' WHERE generation=1"); err == nil || !strings.Contains(err.Error(), "Retained sandbox specifications are immutable") {
		t.Fatal("downgrade left specifications mutable", err)
	}
	if _, err := migrations.DownTo(t.Context(), 86); err == nil || !strings.Contains(err.Error(), "custom endpoint is retained") {
		t.Fatal("downgrade discarded a custom endpoint", err)
	}
}
