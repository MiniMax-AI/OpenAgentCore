package workspacepg_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/workspacepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

type fixture struct {
	pool      *pgxpool.Pool
	store     *workspacepg.Store
	execution *workspacepg.Execution
	lease     *pgunit.Lease
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := pgtest.OpenIsolated(t, nil)
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	f := fixture{pool: pool, store: workspacepg.New(pgunit.NewPool(pool)), execution: workspacepg.NewExecution(lease), lease: lease}
	if err := f.execution.SelectConfiguration(admin(t), newConfiguration()); err != nil {
		t.Fatal(err)
	}
	return f
}

func newConfiguration() workspacefs.Configuration {
	return workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{"storage":"test"}`)}
}

func admin(t *testing.T) context.Context {
	t.Helper()
	return adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ActorLabel: "operator", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
}

func TestConfigurationSelectionRequiresAtomicAudit(t *testing.T) {
	f := newFixture(t)
	before, err := f.store.ActiveConfiguration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next := newConfiguration()
	next.Parameters = []byte(`{"secret":"private-configuration-value"}`)
	if err := f.execution.SelectConfiguration(t.Context(), next); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("selection without audit source", err)
	}
	active, err := f.store.ActiveConfiguration(t.Context())
	if err != nil || active.ID != before.ID {
		t.Fatal("missing audit changed selection", active, err)
	}
	var definitions, operations int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM workspace_fs_configurations), (SELECT count(*) FROM admin_audit_log)`).Scan(&definitions, &operations); err != nil {
		t.Fatal(err)
	}
	if definitions != 1 || operations != 1 {
		t.Fatal("missing audit did not roll back entire mutation", definitions, operations)
	}
	ctx := admin(t)
	for range 2 {
		if err := f.execution.SelectConfiguration(ctx, next); err != nil {
			t.Fatal("immutable selection retry", err)
		}
	}
	var credential, actor, request, trace, action, resource, id, raw string
	var deploymentWide bool
	if err := f.pool.QueryRow(t.Context(), `SELECT admin_credential_id, actor_label, request_id, trace_id, action, resource_type, resource_id,
		tenant_id IS NULL AND project_id IS NULL, row_to_json(a)::text FROM admin_audit_log a WHERE resource_id=$1 LIMIT 1`, next.ID).
		Scan(&credential, &actor, &request, &trace, &action, &resource, &id, &deploymentWide, &raw); err != nil {
		t.Fatal(err)
	}
	source, _ := adminaudit.FromContext(ctx)
	if credential != source.CredentialID || actor != source.ActorLabel || request != source.RequestID || trace != source.TraceID || action != "change" || resource != "workspace_storage" || id != next.ID || !deploymentWide {
		t.Fatal("configuration audit lost administrator provenance")
	}
	if strings.Contains(raw, "private-configuration-value") {
		t.Fatal("configuration secret leaked into audit")
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM workspace_fs_configurations`).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	if definitions != 2 {
		t.Fatal("immutable retry created another definition", definitions)
	}
}

func (f fixture) environment(t *testing.T) (string, string, string) {
	t.Helper()
	tenant, session, environment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration)
		VALUES ($1, $2, 'codex', 'key', 'hash', '{"environment":{"type":"openai_hosted"}}')`, session, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO environments(id, session_id) VALUES ($1, $2)`, environment, session); err != nil {
		t.Fatal(err)
	}
	return tenant, session, environment
}

func (f fixture) bind(t *testing.T) (workspaces.Record, string) {
	t.Helper()
	tenant, session, environment := f.environment(t)
	record, err := f.execution.Bind(t.Context(), tenant, environment)
	if err != nil {
		t.Fatal(err)
	}
	return record, session
}

func attachment(record workspaces.Record) workspacefs.Attachment {
	return workspacefs.Attachment{Reference: record.Reference, ConfigurationID: record.Configuration.ID,
		Kind: workspacefs.AttachmentHostDirectory, Native: []byte(`{"receipt":"fixture"}`)}
}

func (f fixture) deleteSession(t *testing.T, session string) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1`, session); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationSwitchPreservesBinding(t *testing.T) {
	f := newFixture(t)
	first, _ := f.bind(t)
	second := newConfiguration()
	if err := f.execution.SelectConfiguration(admin(t), second); err != nil {
		t.Fatal(err)
	}
	replayed, err := f.execution.Bind(t.Context(), first.Reference.TenantID, first.Reference.EnvironmentID)
	if err != nil || replayed.Reference != first.Reference || replayed.Configuration.ID != first.Configuration.ID {
		t.Fatal("configuration switch changed existing creation", replayed, err)
	}
	fresh, _ := f.bind(t)
	if fresh.Configuration.ID != second.ID || fresh.Reference.ObjectID == first.Reference.ObjectID {
		t.Fatal("new binding did not select fresh configuration", fresh)
	}
	changed := first.Configuration
	changed.Parameters = []byte(`{"storage":"other"}`)
	if err := f.execution.SelectConfiguration(admin(t), changed); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("mutated configuration", err)
	}
	active, err := f.store.ActiveConfiguration(t.Context())
	if err != nil || active.ID != second.ID {
		t.Fatal("rejected selection changed active configuration", active, err)
	}
	if err := f.execution.SelectConfiguration(admin(t), first.Configuration); err != nil {
		t.Fatal("could not reselect retained configuration", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE workspace_fs_configurations SET adapter='other' WHERE id=$1`, first.Configuration.ID); err == nil {
		t.Fatal("database permitted configuration mutation")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE environment_workspaces SET configuration_id=$1 WHERE object_id=$2`, second.ID, first.Reference.ObjectID); err == nil {
		t.Fatal("database permitted rebinding")
	}
}

func TestConcurrentSelectionHasOneActiveConfiguration(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errors <- f.execution.SelectConfiguration(admin(t), newConfiguration()) })
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var active, retained int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE active), count(*) FROM workspace_fs_configurations`).Scan(&active, &retained); err != nil {
		t.Fatal(err)
	}
	if active != 1 || retained != 9 {
		t.Fatal("configuration selection lost retained definitions", active, retained)
	}
}

func TestDeletedSessionRejectsLateReadyAndQueuesWithoutAllocation(t *testing.T) {
	f := newFixture(t)
	record, session := f.bind(t)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), `UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1`, session); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() { ready <- f.execution.MarkReady(t.Context(), record.Reference, attachment(record)) }()
	waitCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := f.pool.QueryRow(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, tx.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-ready:
			t.Fatal("ready publication did not wait for Session deletion", err)
		case <-waitCtx.Done():
			t.Fatal("ready publication did not lock Session", waitCtx.Err())
		case <-ticker.C:
		}
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-ready; !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("late ready accepted deleted Session", err)
	}
	candidates, err := f.store.DeletionCandidates(t.Context(), "")
	if err != nil || len(candidates) != 1 || candidates[0].Reference != record.Reference {
		t.Fatal("unallocated workspace missing from cleanup", candidates, err)
	}
	if err := f.execution.BeginDelete(t.Context(), record.Reference); err != nil {
		t.Fatal(err)
	}
	if err := f.execution.MarkReady(t.Context(), record.Reference, attachment(record)); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("deleting workspace became ready", err)
	}
	if err := f.execution.MarkDeleted(t.Context(), record.Reference); err != nil {
		t.Fatal(err)
	}
	retained, err := f.store.Get(t.Context(), record.Reference.TenantID, record.Reference.EnvironmentID)
	if err != nil || retained.State != workspaces.Deleted || retained.Reference != record.Reference {
		t.Fatal("deleted identity not retained", retained, err)
	}
	if _, err := f.execution.Bind(t.Context(), record.Reference.TenantID, record.Reference.EnvironmentID); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("deleted identity reused", err)
	}
	if err := f.execution.MarkReady(t.Context(), record.Reference, attachment(record)); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("terminal workspace became ready", err)
	}
	candidates, err = f.store.DeletionCandidates(t.Context(), "")
	if err != nil || len(candidates) != 0 {
		t.Fatal("terminal workspace still queued", candidates, err)
	}
}

func TestExpiryArchiveAndFailureDoNotAuthorizeDeletion(t *testing.T) {
	f := newFixture(t)
	for _, status := range []string{"expired", "failed", "disconnected"} {
		record, _ := f.bind(t)
		if err := f.execution.MarkReady(t.Context(), record.Reference, attachment(record)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET status=$2 WHERE id=$1`, record.Reference.EnvironmentID, status); err != nil {
			t.Fatal(err)
		}
		// Managed archive is the released allocation receipt. It is deliberately
		// not Session deletion, regardless of Environment terminal status.
		f.allocation(t, record, true)
		if err := f.execution.BeginDelete(t.Context(), record.Reference); !errors.Is(err, workspaces.ErrConflict) {
			t.Fatal(status, "authorized deletion", err)
		}
	}
	candidates, err := f.store.DeletionCandidates(t.Context(), "")
	if err != nil || len(candidates) != 0 {
		t.Fatal("nondeleted Sessions queued", candidates, err)
	}
}

func TestDeletionWaitsForComputeAndSnapshotRelease(t *testing.T) {
	f := newFixture(t)
	record, session := f.bind(t)
	f.allocation(t, record, false)
	f.deleteSession(t, session)
	candidates, err := f.store.DeletionCandidates(t.Context(), "")
	if err != nil || len(candidates) != 0 {
		t.Fatal("unreleased snapshot queued filesystem deletion", candidates, err)
	}
	if err := f.execution.BeginDelete(t.Context(), record.Reference); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("deleted filesystem before releasing compute", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET state='released', released_at=clock_timestamp(), create_settled=true WHERE environment_id=$1`, record.Reference.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	candidates, err = f.store.DeletionCandidates(t.Context(), "")
	if err != nil || len(candidates) != 1 {
		t.Fatal("released ownership did not enable deletion", candidates, err)
	}
	if err := f.execution.BeginDelete(t.Context(), record.Reference); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) allocation(t *testing.T, record workspaces.Record, released bool) {
	t.Helper()
	device := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO devices(id,tenant_id,name,credential_hash) VALUES($1,$2,'fixture',$3)`, device, record.Reference.TenantID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,deployment_generation,state,create_settled,released_at,compute_phase,compute_state)
		VALUES($1,$2,$3,$4,0,CASE WHEN $5::boolean THEN 'released' ELSE 'cleanup_pending' END,$5,CASE WHEN $5 THEN clock_timestamp() ELSE NULL END,'suspended','{"snapshot":{"receipt":"owned"}}')`,
		uuid.NewString(), record.Reference.EnvironmentID, device, uuid.NewString(), released); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCreationRetainedUntilExplicitSessionDeletion(t *testing.T) {
	f := newFixture(t)
	record, session := f.bind(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET status='failed' WHERE id=$1`, record.Reference.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	candidates, err := f.store.DeletionCandidates(t.Context(), "")
	if err != nil || len(candidates) != 0 {
		t.Fatal("failure alone authorized cleanup", candidates, err)
	}
	if err := f.execution.BeginDelete(t.Context(), record.Reference); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("failed creation allowed deletion without Session deletion", err)
	}
	f.deleteSession(t, session)
	if err := f.execution.BeginDelete(t.Context(), record.Reference); err != nil {
		t.Fatal(err)
	}
	candidates, err = f.store.DeletionCandidates(t.Context(), "")
	if err != nil || len(candidates) != 1 {
		t.Fatal("explicit Session deletion not queued", candidates, err)
	}
	if err := f.execution.MarkDeleted(t.Context(), record.Reference); err != nil {
		t.Fatal(err)
	}
	if _, err := f.execution.Bind(t.Context(), record.Reference.TenantID, record.Reference.EnvironmentID); !errors.Is(err, workspaces.ErrConflict) {
		t.Fatal("deleted identity reused", err)
	}
}

func TestTenantAndAttachmentOwnership(t *testing.T) {
	f := newFixture(t)
	record, _ := f.bind(t)
	foreign := record.Reference
	foreign.TenantID = uuid.NewString()
	if _, err := f.store.Get(t.Context(), foreign.TenantID, foreign.EnvironmentID); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatal("foreign tenant read workspace", err)
	}
	if _, err := f.execution.Bind(t.Context(), foreign.TenantID, foreign.EnvironmentID); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatal("foreign tenant rebound workspace", err)
	}
	if err := f.execution.MarkReady(t.Context(), foreign, attachment(record)); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatal("foreign tenant wrote workspace", err)
	}
	if err := f.execution.BeginDelete(t.Context(), foreign); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatal("foreign tenant deleted workspace", err)
	}
	for _, wrong := range []string{"configuration", "object"} {
		a := attachment(record)
		if wrong == "configuration" {
			a.ConfigurationID = uuid.NewString()
		} else {
			a.Reference.ObjectID = uuid.NewString()
		}
		if err := f.execution.MarkReady(t.Context(), record.Reference, a); !errors.Is(err, workspacefs.ErrOwnership) {
			t.Fatal("accepted wrong attachment", wrong, err)
		}
	}
	if err := f.execution.MarkReady(t.Context(), record.Reference, attachment(record)); err != nil {
		t.Fatal(err)
	}
	stored, err := f.store.Get(t.Context(), record.Reference.TenantID, record.Reference.EnvironmentID)
	if err != nil || stored.State != workspaces.Ready || stored.Attachment == nil {
		t.Fatal("valid attachment not stored", stored, err)
	}
}

func TestWorkspaceWritesRequireExecutionLease(t *testing.T) {
	f := newFixture(t)
	record, _ := f.bind(t)
	if err := f.lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func() error{
		"configuration": func() error { return f.execution.SelectConfiguration(admin(t), newConfiguration()) },
		"bind": func() error {
			_, err := f.execution.Bind(t.Context(), record.Reference.TenantID, record.Reference.EnvironmentID)
			return err
		},
		"ready":   func() error { return f.execution.MarkReady(t.Context(), record.Reference, attachment(record)) },
		"delete":  func() error { return f.execution.BeginDelete(t.Context(), record.Reference) },
		"deleted": func() error { return f.execution.MarkDeleted(t.Context(), record.Reference) },
	} {
		if err := write(); !errors.Is(err, pgunit.ErrLeaseClosed) {
			t.Fatal(name, "wrote without lease", err)
		}
	}
}
