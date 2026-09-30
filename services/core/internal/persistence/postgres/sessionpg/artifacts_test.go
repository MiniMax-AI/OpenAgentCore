package sessionpg

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func uuidText(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

// stagingTurn stores a fresh tenant's Session with a connected Environment of
// kind and an in-progress Turn, and returns their IDs.
func stagingTurn(t *testing.T, pool *pgxpool.Pool, kind string) (tenant, session, environment, turn string) {
	t.Helper()
	tenantID, sessionID, environmentID := newEnvironment(t, pool, kind, "connected")
	turnID := uuid.New()
	exec(t, pool, `INSERT INTO turns(id, session_id, status) VALUES ($1, $2, 'in_progress')`, turnID, sessionID)
	return uuidText(tenantID), uuidText(sessionID), uuidText(environmentID), turnID.String()
}

// artifactExport is a Turn export of files in order.
func artifactExport(t *testing.T, files ...[2]string) []byte {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	for _, file := range files {
		if err := w.WriteHeader(&tar.Header{Name: file[0], Mode: 0600, Size: int64(len(file[1])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func largeObjects(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_largeobject_metadata").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func stagedRows(t *testing.T, pool *pgxpool.Pool, turn string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM session_artifacts WHERE turn_id = $1", turn).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func stagingService(t *testing.T, pool *pgxpool.Pool) (*Store, *sessions.Service) {
	t.Helper()
	store := New(pgunit.NewPool(pool))
	service, err := sessions.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	return store, service
}

func stage(t *testing.T, service *sessions.Service, tenant, session, turn, environment string, export io.Reader) error {
	return service.StageTurnArtifacts(t.Context(), sessions.StageTurnArtifactsCommand{TenantID: tenant, SessionID: session, TurnID: turn, EnvironmentID: environment, Export: export})
}

type artifactReadError struct{}

func (artifactReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// A rejected or unauthorized export leaves no content and no Artifact. The
// database is isolated because the check counts every large object.
func TestArtifactStagingStoresNothingItRejects(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	_, service := stagingService(t, pool)
	for _, kind := range []string{"openai_hosted", "self_hosted"} {
		t.Run(kind, func(t *testing.T) {
			tenant, session, environment, turn := stagingTurn(t, pool, kind)
			before := largeObjects(t, pool)
			valid := artifactExport(t, [2]string{"outputs/a", "data"})
			for name, body := range map[string]io.Reader{
				"transport-failure-after-valid-tar": io.MultiReader(bytes.NewReader(valid), artifactReadError{}),
				"truncated-body":                    bytes.NewReader(valid[:513]),
				"trailing-data":                     io.MultiReader(bytes.NewReader(valid), bytes.NewReader([]byte("not archive padding"))),
				"traversal":                         bytes.NewReader(artifactExport(t, [2]string{"outputs/../secret", "no"})),
				"private-root":                      bytes.NewReader(artifactExport(t, [2]string{"outputs/a", "data"}, [2]string{"secrets/key", "no"})),
			} {
				t.Run(name, func(t *testing.T) {
					if err := stage(t, service, tenant, session, turn, environment, body); err == nil {
						t.Fatal("invalid capture accepted")
					}
					if count := largeObjects(t, pool); count != before {
						t.Fatalf("rollback leaked objects: %d -> %d", before, count)
					}
					if rows := stagedRows(t, pool, turn); rows != 0 {
						t.Fatalf("rollback kept %d Artifacts", rows)
					}
				})
			}
			// Authorization precedes reading the export.
			for _, ids := range [][4]string{{uuid.NewString(), session, turn, environment}, {tenant, session, turn, uuid.NewString()}} {
				if err := stage(t, service, ids[0], ids[1], ids[2], ids[3], artifactReadError{}); !errors.Is(err, sessions.ErrNotFound) {
					t.Fatalf("unauthorized capture reached the export: %v", err)
				}
			}
		})
	}
}

// Staged content stays private, and a Turn that stopped running or a deleted
// Session stages nothing.
func TestArtifactStagingRequiresTheRunningTurn(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	store, service := stagingService(t, pool)
	tenant, session, environment, turn := stagingTurn(t, pool, "openai_hosted")
	before := largeObjects(t, pool)
	body := artifactExport(t, [2]string{"outputs/a.txt", "alpha"}, [2]string{"outputs/empty", ""})
	if err := stage(t, service, tenant, session, turn, environment, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	var private, total int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE created_at IS NULL AND session_id = $2 AND environment_id = $3), count(*)
		FROM session_artifacts WHERE turn_id = $1`, turn, session, environment).Scan(&private, &total); err != nil || private != 2 || total != 2 {
		t.Fatalf("staged %d private of %d: %v", private, total, err)
	}
	if count := largeObjects(t, pool); count != before+2 {
		t.Fatalf("staged content: %d -> %d", before, count)
	}
	if page, err := store.ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true); err != nil || len(page.Artifacts) != 0 {
		t.Fatalf("private capture visible: %+v %v", page, err)
	}

	exec(t, pool, `UPDATE turns SET cancel_requested_at = clock_timestamp() WHERE id = $1`, turn)
	late := artifactExport(t, [2]string{"outputs/late", "late"})
	if err := stage(t, service, tenant, session, turn, environment, bytes.NewReader(late)); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatalf("capture after a cancel request: %v", err)
	}
	exec(t, pool, `UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1`, session)
	if err := stage(t, service, tenant, session, turn, environment, bytes.NewReader(late)); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("capture for a deleted Session: %v", err)
	}
	if count := largeObjects(t, pool); count != before+2 {
		t.Fatalf("rejected capture leaked objects: %d -> %d", before+2, count)
	}
	if rows := stagedRows(t, pool, turn); rows != 2 {
		t.Fatalf("rejected capture changed Artifacts: %d", rows)
	}
}

// The Artifact list pages in publication order in both directions, cursors
// stay within the Session, and a read admitted before a deletion finishes.
func TestSessionArtifactReadsPageReadAndDelete(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	tenant, session, environment, turn := stagingTurn(t, pool, "openai_hosted")
	body := artifactExport(t, [2]string{"outputs/a", "alpha"}, [2]string{"outputs/b", "bravo"}, [2]string{"outputs/c", "charlie"})
	if err := stage(t, service, tenant, session, turn, environment, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	// Publication belongs to Turn completion; publish in path order here.
	exec(t, pool, `UPDATE session_artifacts SET created_at = timestamptz '2026-01-01' + (ascii(right(path, 1)) * interval '1 second') WHERE turn_id = $1`, turn)
	otherTenant, otherSession, otherEnvironment, otherTurn := stagingTurn(t, pool, "openai_hosted")
	if err := stage(t, service, otherTenant, otherSession, otherTurn, otherEnvironment, bytes.NewReader(artifactExport(t, [2]string{"outputs/x", "x"}))); err != nil {
		t.Fatal(err)
	}
	exec(t, pool, `UPDATE session_artifacts SET created_at = clock_timestamp() WHERE turn_id = $1`, otherTurn)
	foreign, err := store.ListSessionArtifacts(t.Context(), otherTenant, otherSession, "", "", 100, true)
	if err != nil || len(foreign.Artifacts) != 1 {
		t.Fatal(foreign, err)
	}

	for _, test := range []struct {
		ascending bool
		want      string
	}{{true, "abc"}, {false, "cba"}} {
		var got string
		cursor := ""
		for pages := 0; ; pages++ {
			page, err := store.ListSessionArtifacts(t.Context(), tenant, session, environment, cursor, 1, test.ascending)
			if err != nil || len(page.Artifacts) != 1 || pages > 3 {
				t.Fatal(page, err)
			}
			got += page.Artifacts[0].Path[len("/workspace/outputs/"):]
			if cursor = page.NextCursor; cursor == "" {
				break
			}
		}
		if got != test.want {
			t.Fatalf("ascending=%v paged %q, want %q", test.ascending, got, test.want)
		}
	}
	for _, limit := range []int{0, 101} {
		if _, err := store.ListSessionArtifacts(t.Context(), tenant, session, "", "", limit, true); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	for _, cursor := range []string{"not-an-id", foreign.Artifacts[0].ID} {
		if _, err := store.ListSessionArtifacts(t.Context(), tenant, session, "", cursor, 10, true); !errors.Is(err, sessions.ErrArtifactCursor) {
			t.Fatalf("cursor %q: %v", cursor, err)
		}
	}
	if _, err := store.ListSessionArtifacts(t.Context(), otherTenant, session, "", "", 10, true); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("foreign tenant: %v", err)
	}
	if page, err := store.ListSessionArtifacts(t.Context(), tenant, session, "not-an-id", "", 10, true); err != nil || len(page.Artifacts) != 0 {
		t.Fatalf("malformed environment filter: %+v %v", page, err)
	}

	page, err := store.ListSessionArtifacts(t.Context(), tenant, session, "", "", 10, true)
	if err != nil || len(page.Artifacts) != 3 {
		t.Fatal(page, err)
	}
	first := page.Artifacts[0]
	if got, err := store.GetSessionArtifact(t.Context(), tenant, session, first.ID); err != nil || got.ID != first.ID || got.Path != "/workspace/outputs/a" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := store.GetSessionArtifact(t.Context(), otherTenant, session, first.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("foreign get: %v", err)
	}
	var oid uint32
	if err := pool.QueryRow(t.Context(), "SELECT body_oid FROM session_artifacts WHERE id = $1", first.ID).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSessionArtifact(t.Context(), sessions.DeleteSessionArtifactCommand{TenantID: otherTenant, SessionID: session, ArtifactID: first.ID}); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if err := store.ReadSessionArtifact(t.Context(), tenant, session, first.ID, func(read sessions.Artifact, content io.Reader) error {
		if err := service.DeleteSessionArtifact(t.Context(), sessions.DeleteSessionArtifactCommand{TenantID: tenant, SessionID: session, ArtifactID: first.ID}); err != nil {
			return err
		}
		data, err := io.ReadAll(content)
		if err != nil || read.ID != first.ID || string(data) != "alpha" {
			t.Fatalf("admitted read: %+v %q %v", read, data, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionArtifact(t.Context(), tenant, session, first.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("deleted Artifact: %v", err)
	}
	var content bool
	if err := pool.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_largeobject_metadata WHERE oid = $1)", oid).Scan(&content); err != nil || content {
		t.Fatalf("deleted content kept: %v %v", content, err)
	}
	if err := service.DeleteSessionArtifact(t.Context(), sessions.DeleteSessionArtifactCommand{TenantID: tenant, SessionID: session, ArtifactID: first.ID}); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("repeated delete: %v", err)
	}
}
