package store

import (
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestRuntimeNodeCapacityMigrationPreservesExistingConfiguration(t *testing.T) {
	s, _, d := managerFixture(t, 7, 29)
	db := sql.OpenDB(stdlib.GetConnector(*s.pool.Config().ConnConfig))
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 69); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetRuntimeNode(t.Context(), d.LocalNodeID)
	if err != nil || current.AdmissionState != "enabled" || current.MaxActive != 7 || current.MaxRetained != 29 || !current.Schedulable {
		t.Fatal("upgrade changed persisted node capacity", current, err)
	}
	id := uuid.NewString()
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO runtime_nodes(id,installation_id,name,backend_fingerprint,credential_sha256,max_active,max_retained,specification_digest,deployment_generation) SELECT $1,installation_id,'new default',backend_fingerprint,credential_sha256,1,1,specification_digest,deployment_generation FROM runtime_nodes WHERE id=$2`, id, d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	pending, err := s.GetRuntimeNode(t.Context(), id)
	if err != nil || pending.AdmissionState != "pending_confirmation" {
		t.Fatal("new row defaults enabled", pending, err)
	}
}
