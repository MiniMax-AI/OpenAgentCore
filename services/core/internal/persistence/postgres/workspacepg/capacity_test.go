package workspacepg_test

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/workspacepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

const syntheticKey = "synthetic-private-ssh-key"

func credentialed() workspacefs.ConfigurationRecord {
	c := newConfiguration()
	c.Credential = []byte(`{"ssh_private_key":"` + syntheticKey + `"}`)
	return c
}

func TestConfigurationCredentialIsSealedAndWriteOnly(t *testing.T) {
	f := newFixture(t)
	config := credentialed()
	f.selectConfiguration(t, config, 4096)
	var stored []byte
	var audit string
	if err := f.pool.QueryRow(t.Context(), `SELECT credential, (SELECT string_agg(row_to_json(a)::text, '') FROM admin_audit_log a) FROM workspace_fs_configurations WHERE id=$1`, config.ID).Scan(&stored, &audit); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 || bytes.Contains(stored, []byte(syntheticKey)) || strings.Contains(audit, syntheticKey) {
		t.Fatal("credential stored or audited in plaintext")
	}
	active, err := f.store.ActiveConfiguration(t.Context())
	if err != nil || !bytes.Equal(active.Credential, config.Credential) {
		t.Fatal("Control record did not open its credential", err)
	}
	selection, err := f.store.Selection(t.Context())
	if err != nil || selection.ID != config.ID || selection.TenantCapacityMiB != 4096 {
		t.Fatal("selection", selection, err)
	}
	other := workspacepg.New(pgunit.NewPool(f.pool), f.cipher, uuid.NewString())
	if _, err := other.ActiveConfiguration(t.Context()); err == nil {
		t.Fatal("credential opened for another installation")
	}
	if _, err := other.Selection(t.Context()); err != nil {
		t.Fatal("public selection required the credential", err)
	}
	// The same ID is immutable, including its credential.
	f.selectConfiguration(t, config, 4096)
	for _, credential := range [][]byte{nil, []byte(`{"ssh_private_key":"another"}`)} {
		changed := config
		changed.Credential = credential
		if err := f.execution.SelectConfiguration(admin(t), changed, 4096); !errors.Is(err, workspaces.ErrConflict) {
			t.Fatal("credential of a retained configuration changed", err)
		}
	}
	retained, err := f.store.Configuration(t.Context(), config.ID)
	if err != nil || !bytes.Equal(retained.Credential, config.Credential) {
		t.Fatal("retained configuration credential", err)
	}
	if _, err := f.store.Configuration(t.Context(), uuid.NewString()); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatal("absent configuration", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE workspace_fs_configurations SET credential=NULL WHERE id=$1`, config.ID); err == nil {
		t.Fatal("database permitted credential mutation")
	}
}

func (f fixture) bindTenant(t *testing.T, tenant string, capacity uint32) (workspaces.Record, string, error) {
	t.Helper()
	session, environment := uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration)
		VALUES ($1, $2, 'codex', $3, 'hash', '{"environment":{"type":"openai_hosted"}}')`, session, tenant, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO environments(id, session_id) VALUES ($1, $2)`, environment, session); err != nil {
		t.Fatal(err)
	}
	record, err := f.execution.Bind(t.Context(), tenant, environment, *f.active, capacity)
	return record, session, err
}

func TestTenantCapacityAdmission(t *testing.T) {
	f := newFixture(t)
	f.selectConfiguration(t, credentialed(), 7*1024)
	tenant, other := uuid.NewString(), uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			_, _, err := f.bindTenant(t, tenant, 1024)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	admitted, refused := 0, 0
	for err := range results {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, workspaces.ErrCapacityExhausted) && errors.Is(err, workspacefs.ErrUnavailable):
			refused++
		default:
			t.Fatal(err)
		}
	}
	if admitted != 7 || refused != 3 {
		t.Fatal("tenant capacity admitted", admitted, "refused", refused)
	}
	if _, _, err := f.bindTenant(t, other, 7*1024); err != nil {
		t.Fatal("another tenant's capacity was consumed", err)
	}
	if _, _, err := f.bindTenant(t, other, 0); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal("unbounded workspace admitted under a tenant capacity", err)
	}

	// A configuration switch keeps every unreleased capacity counted.
	f.selectConfiguration(t, credentialed(), 7*1024)
	if _, _, err := f.bindTenant(t, tenant, 1024); !errors.Is(err, workspaces.ErrCapacityExhausted) {
		t.Fatal("configuration switch reset tenant usage", err)
	}
	var creating, ready workspaces.Record
	var session string
	if err := f.pool.QueryRow(t.Context(), `SELECT e.session_id FROM environment_workspaces w JOIN environments e ON e.id=w.environment_id JOIN sessions s ON s.id=e.session_id WHERE s.tenant_id=$1 LIMIT 1`, tenant).Scan(&session); err != nil {
		t.Fatal(err)
	}
	var environment string
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM environments WHERE session_id=$1`, session).Scan(&environment); err != nil {
		t.Fatal(err)
	}
	creating, err := f.store.Get(t.Context(), tenant, environment)
	if err != nil {
		t.Fatal(err)
	}
	// Retained bindings bypass admission, including after a lower cap.
	f.selectConfiguration(t, credentialed(), 1024)
	if ready, err = f.rebind(t, creating); err != nil || ready.Reference != creating.Reference || ready.CapacityMiB != 1024 {
		t.Fatal("retained binding blocked by a lower cap", err)
	}
	// Deleting still counts; physical release frees capacity.
	f.selectConfiguration(t, credentialed(), 7*1024)
	f.deleteSession(t, session)
	if err := f.execution.BeginDelete(t.Context(), creating.Reference); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.bindTenant(t, tenant, 1024); !errors.Is(err, workspaces.ErrCapacityExhausted) {
		t.Fatal("deleting workspace released capacity before confirmation", err)
	}
	if err := f.execution.MarkDeleted(t.Context(), creating.Reference); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.bindTenant(t, tenant, 1024); err != nil {
		t.Fatal("confirmed release did not free capacity", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE environment_workspaces SET capacity_mib=capacity_mib+1 WHERE object_id=$1`, creating.Reference.ObjectID); err == nil {
		t.Fatal("database permitted admitted capacity mutation")
	}
}

func TestRetainedUnboundedWorkspaceBlocksTenantAdmission(t *testing.T) {
	f := newFixture(t)
	tenant, other := uuid.NewString(), uuid.NewString()
	unbounded, _, err := f.bindTenant(t, tenant, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.selectConfiguration(t, credentialed(), 8192)
	if _, _, err := f.bindTenant(t, tenant, 1024); !errors.Is(err, workspaces.ErrCapacityExhausted) {
		t.Fatal("tenant with an unbounded workspace admitted under a cap", err)
	}
	if _, _, err := f.bindTenant(t, other, 1024); err != nil {
		t.Fatal("unrelated tenant blocked", err)
	}
	if replay, err := f.rebind(t, unbounded); err != nil || replay.Reference != unbounded.Reference || replay.CapacityMiB != 0 {
		t.Fatal("existing unbounded workspace blocked or resized", err)
	}
}

func TestBindFencesConfigurationSwitch(t *testing.T) {
	f := newFixture(t)
	stale := *f.active
	f.selectConfiguration(t, newConfiguration(), 0)
	tenant, _, environment := f.environment(t)
	if _, err := f.execution.Bind(t.Context(), tenant, environment, stale, 0); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("bound a configuration that is no longer selected", err)
	}
	if _, err := f.store.Get(t.Context(), tenant, environment); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatal("conflicting bind left a row", err)
	}
	record, err := f.execution.Bind(t.Context(), tenant, environment, *f.active, 2048)
	if err != nil || record.CapacityMiB != 2048 || record.Configuration.ID != *f.active {
		t.Fatal(record, err)
	}
}
