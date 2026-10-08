package integration

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestAgentHostMigration(t *testing.T) {
	for _, scenario := range []string{"bound_assignment", "subagent_history", "pending_write", "live_allocation", "enrollment_authority", "settled_session"} {
		t.Run(scenario, func(t *testing.T) {
			s, pool := newManagedTestStore(t)
			ctx := t.Context()
			tenant, session := newTurnSession(t, s)
			db := sql.OpenDB(stdlib.GetConnector(*pool.Config().ConnConfig))
			t.Cleanup(func() { _ = db.Close() })
			migrations, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := migrations.DownTo(ctx, 97); err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := func(table string) string {
				t.Helper()
				var value string
				if err := pool.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text), '[]'::jsonb)::text FROM "+table+" r").Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			device, turn, environment := uuid.NewString(), uuid.NewString(), uuid.NewString()
			exec(`INSERT INTO devices(id, tenant_id, name, credential_hash) VALUES($1,$2,'guest',$3)`, device, tenant, strings.Repeat("a", 64))
			exec(`INSERT INTO turns(id,session_id,status,started_at,completed_at) VALUES($1,$2,'completed',clock_timestamp(),clock_timestamp())`, turn, session.ID)
			exec(`INSERT INTO session_runtime_assignments(session_id,runtime_id,native_session_id) VALUES($1,$2,'native-history')`, session.ID, device)
			if scenario == "bound_assignment" {
				before := snapshot("devices")
				if _, err := migrations.Up(ctx); err == nil || !strings.Contains(err.Error(), "delete those Sessions, then upgrade") {
					t.Fatal("upgrade accepted a bound Session", err)
				}
				if snapshot("devices") != before {
					t.Fatal("refused upgrade changed devices")
				}
			}
			exec(`UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1`, session.ID)
			if scenario == "subagent_history" || scenario == "pending_write" || scenario == "live_allocation" {
				exec(`INSERT INTO environments(id,session_id) VALUES($1,$2)`, environment, session.ID)
				if scenario == "subagent_history" {
					exec(`INSERT INTO turn_events(session_id,turn_id,ordinal,kind,payload) VALUES($1,$2,1,'subagent','{}')`, session.ID, turn)
					exec(`INSERT INTO subagent_identities(id,session_id,device_id,engine,native_id,parent_native_id,native_created_at,first_turn_id,first_event_ordinal) VALUES($1,$2,$3,'codex','child','root',1,$4,1)`, uuid.NewString(), session.ID, device, turn)
				}
				if scenario == "pending_write" {
					exec(`INSERT INTO environment_file_writes(id,environment_id,device_id,request_sha256) VALUES($1,$2,$3,$4)`, uuid.NewString(), environment, device, strings.Repeat("b", 64))
				}
				if scenario == "live_allocation" {
					// The allocation still owns a real resource, even after Session deletion.
					exec(`INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key,deployment_generation) VALUES($1,$2,$3,$4,0)`, uuid.NewString(), environment, device, uuid.NewString())
				}
			}
			histories := map[string]string{}
			for _, table := range []string{"sessions", "turns", "turn_events", "subagent_identities", "environment_file_writes"} {
				histories[table] = snapshot(table)
			}
			if _, err := migrations.Up(ctx); err != nil {
				t.Fatal(err)
			}
			for table, before := range histories {
				if snapshot(table) != before {
					t.Fatalf("upgrade changed %s", table)
				}
			}
			if snapshot("devices") != "[]" || snapshot("session_runtime_assignments") != "[]" {
				t.Fatal("guest execution authority survived upgrade")
			}
			if scenario == "enrollment_authority" {
				principal := FixtureExecutorPrincipal(t, s, uuid.NewString())
				selfHosted, err := s.CreateSession(ctx, principal.TenantID, sessions.CreateSession{
					Creator: principal.Subject(), Engine: "codex", IdempotencyKey: uuid.NewString(),
					Configuration: json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`),
				})
				if err != nil {
					t.Fatal(err)
				}
				service := sessionService(t, s)
				first, err := service.IssueExecutorCredential(ctx, principal, uuid.NewString(), selfHosted.Environment.ID)
				if err != nil {
					t.Fatal(err)
				}
				resource, err := service.EnrollRuntime(ctx, selfHosted.Environment.ID, executorDigest(first.Token))
				if err != nil {
					t.Fatal(err)
				}
				before := snapshot("sandbox_enrollments")
				if _, err := migrations.DownTo(ctx, 97); err == nil || !strings.Contains(err.Error(), "Cannot restore device constraints") {
					t.Fatal("rollback discarded enrollment authority", err)
				}
				if snapshot("sandbox_enrollments") != before || snapshot("session_runtime_assignments") != "[]" || snapshot("runtime_allocations") != "[]" {
					t.Fatal("refused rollback changed unbound enrollment")
				}
				var columns int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='devices' AND column_name='credential_revision'`).Scan(&columns); err != nil || columns != 1 {
					t.Fatal("refused rollback changed schema", columns, err)
				}
				if again, err := service.EnrollRuntime(ctx, selfHosted.Environment.ID, executorDigest(first.Token)); err != nil || again != resource {
					t.Fatal("rollback changed enrolled identity", again, err)
				}
				return
			}
			if scenario == "subagent_history" || scenario == "pending_write" || scenario == "live_allocation" {
				if scenario == "live_allocation" {
					var count int
					if err := pool.QueryRow(ctx, `SELECT count(*) FROM runtime_allocations WHERE state='creating' AND NOT create_settled`).Scan(&count); err != nil || count != 1 {
						t.Fatal("upgrade discarded Provider cleanup", count, err)
					}
				}
				before := snapshot("runtime_allocations")
				if _, err := migrations.DownTo(ctx, 97); err == nil || !strings.Contains(err.Error(), "Cannot restore device constraints") {
					t.Fatal("rollback discarded history", err)
				}
				for table, original := range histories {
					if snapshot(table) != original {
						t.Fatalf("refused rollback changed %s", table)
					}
				}
				if snapshot("runtime_allocations") != before {
					t.Fatal("refused rollback changed allocation")
				}
				var columns int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='devices' AND column_name='credential_revision'`).Scan(&columns); err != nil || columns != 1 {
					t.Fatal("refused rollback changed schema", columns, err)
				}
				return
			}
			// A host has run the Session and acknowledged its deletion. The root Turn
			// remains history, while the settled execution association can be removed.
			host := uuid.NewString()
			exec(`INSERT INTO devices(id,name,credential_hash) VALUES($1,'host',$2)`, host, strings.Repeat("c", 64))
			exec(`INSERT INTO session_runtime_assignments(session_id,runtime_id,native_session_id,desired_state,remove_home,epoch,applied_epoch) VALUES($1,$2,'native-host-history','released',true,2,2)`, session.ID, host)
			if scenario == "settled_session" {
				exec(`UPDATE session_runtime_assignments SET applied_epoch=1 WHERE session_id=$1`, session.ID)
				before := snapshot("session_runtime_assignments")
				hosts := snapshot("devices")
				if _, err := migrations.DownTo(ctx, 97); err == nil || !strings.Contains(err.Error(), "Cannot restore device constraints") {
					t.Fatal("rollback discarded pending home cleanup", err)
				}
				if snapshot("session_runtime_assignments") != before || snapshot("devices") != hosts {
					t.Fatal("refused rollback changed pending cleanup")
				}
				exec(`UPDATE session_runtime_assignments SET applied_epoch=epoch WHERE session_id=$1`, session.ID)
			}
			if _, err := migrations.DownTo(ctx, 97); err != nil {
				t.Fatal("settled Session prevented rollback", err)
			}
			if _, err := migrations.Up(ctx); err != nil {
				t.Fatal("second upgrade", err)
			}
			for table, before := range histories {
				if snapshot(table) != before {
					t.Fatalf("round trip changed %s", table)
				}
			}
		})
	}
}
