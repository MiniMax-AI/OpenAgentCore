package store

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

func TestEnvironmentUploadWriteAuditSurvivesRequestAndLeaseContext(t *testing.T) {
	f := newFileWriteFixture(t)
	ctx, cancel := context.WithCancel(sessionAuditContext(t, f.tenant, "a"))
	source, _ := writeaudit.FromContext(ctx)
	if _, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key); err != nil {
		t.Fatal(err)
	}
	cancel()
	sessionAuditCount(t, f.s, f.tenant, 0)
	// Neither the settlement caller nor the newly acquired execution lease owns the request context.
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, f.writer.pool)
	if err := f.writer.lease.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	next := executionWriter(t, f.s)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if _, err := next.SettleEnvironmentFileWrite(t.Context(), f.tenant, f.env.ID, f.key, "committed"); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	sessionAuditCount(t, f.s, f.tenant, 1)
	var key, request, trace, resource, parent, action string
	if err := f.s.pool.QueryRow(t.Context(), `SELECT key_id,request_id,trace_id,resource_id,parent_id,action FROM write_audit_operations WHERE tenant_id=$1`, f.tenant).Scan(&key, &request, &trace, &resource, &parent, &action); err != nil || key != source.KeyID || request != source.RequestID || trace != source.TraceID || resource != f.env.ID || parent != f.session.ID || action != "upload_file" {
		t.Fatal("durable source lost", key, request, trace, resource, parent, action, err)
	}
}

func TestEnvironmentUploadWriteAuditRollbackAndRejection(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "reject"}[rejected], func(t *testing.T) {
			f := newFileWriteFixture(t)
			ctx := sessionAuditContext(t, f.tenant, "a")
			rejectSessionAudit(t, f.s, ctx)
			if _, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key); err != nil {
				t.Fatal(err)
			}
			state := "committed"
			if rejected {
				state = "rejected"
			}
			_, err := f.writer.SettleEnvironmentFileWrite(t.Context(), f.tenant, f.env.ID, f.key, state)
			if rejected && err != nil || !rejected && err == nil {
				t.Fatal("unexpected settlement outcome", err)
			}
			got, err := f.s.GetEnvironmentFileWrite(t.Context(), f.tenant, f.env.ID, f.key.ID)
			want := "pending"
			if rejected {
				want = "rejected"
			}
			if err != nil || got.State != want {
				t.Fatal("settlement did not roll back", got, err)
			}
			sessionAuditCount(t, f.s, f.tenant, 0)
		})
	}
}

func TestArtifactDeleteWriteAuditAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			s, _ := testStore(t)
			tenant, session, env, turn := artifactTurn(t, s, "self_hosted")
			archive := artifactArchive(t, map[string][]byte{"outputs/private.txt": []byte("secret-file-body")})
			if err := stageTurnArtifacts(t.Context(), s, tenant, session, turn, env, bytes.NewReader(archive)); err != nil {
				t.Fatal(err)
			}
			transition(t, s, tenant, session, turn, sessions.TurnInProgress, sessions.TurnCompleted)
			page, err := sessionAdapter(s).ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true)
			if err != nil || len(page.Artifacts) != 1 {
				t.Fatal(page, err)
			}
			artifact := page.Artifacts[0]
			ctx := sessionAuditContext(t, tenant, "a")
			if fail {
				rejectSessionAudit(t, s, ctx)
			}
			err = sessionAdapter(s).DeleteSessionArtifact(ctx, tenant, session, artifact.ID)
			if fail {
				if err == nil {
					t.Fatal("artifact deletion bypassed audit failure")
				}
				if err := sessionAdapter(s).ReadSessionArtifact(t.Context(), tenant, session, artifact.ID, func(_ sessions.Artifact, r io.Reader) error {
					body, err := io.ReadAll(r)
					if string(body) != "secret-file-body" {
						t.Error("large object did not roll back")
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				sessionAuditCount(t, s, tenant, 0)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				sessionAuditCount(t, s, tenant, 1)
				var resource, parent, raw string
				if err := s.pool.QueryRow(t.Context(), `SELECT resource_id,parent_id,to_jsonb(o)::text FROM write_audit_operations o WHERE tenant_id=$1`, tenant).Scan(&resource, &parent, &raw); err != nil || resource != artifact.ID || parent != session || strings.Contains(raw, "secret-file-body") || strings.Contains(raw, "outputs/") {
					t.Fatal("artifact audit identity or privacy", err)
				}
			}
		})
	}
}
