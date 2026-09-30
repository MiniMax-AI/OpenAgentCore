package store

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
)

func artifactArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	for name, body := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func artifactTurn(t *testing.T, s *Store, kind string) (tenant, session, environment, turn string) {
	t.Helper()
	tenant = uuid.NewString()
	created, err := s.CreateSession(t.Context(), tenant, environmentInput("artifact", kind, "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.GetSessionEnvironment(t.Context(), tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := submitMessage(t, s, tenant, created.ID, "artifact-turn")
	transition(t, s, tenant, created.ID, input.TurnID, TurnQueued, TurnInProgress)
	return tenant, created.ID, env.ID, input.TurnID
}

func TestSessionArtifactsPublishVersionScopeAndLifetime(t *testing.T) {
	for _, kind := range []string{"openai_hosted", "self_hosted"} {
		t.Run(kind, func(t *testing.T) { testSessionArtifactsPublishVersionScopeAndLifetime(t, kind) })
	}
}

func testSessionArtifactsPublishVersionScopeAndLifetime(t *testing.T, kind string) {
	s, pool := testStore(t)
	tenant, session, environment, turn := artifactTurn(t, s, kind)
	before := sourceObjectCount(t, pool)
	data := bytes.Repeat([]byte("immutable\x00"), 100000)
	archive := artifactArchive(t, map[string][]byte{"outputs/a.bin": data, "outputs/nested/empty": {}})
	if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true)
	if err != nil || len(page.Artifacts) != 0 {
		t.Fatalf("private capture visible: %+v %v", page, err)
	}
	completed := transition(t, s, tenant, session, turn, TurnInProgress, TurnCompleted)
	page, err = s.ListSessionArtifacts(t.Context(), tenant, session, environment, "", 100, true)
	if err != nil || len(page.Artifacts) != 2 {
		t.Fatalf("published capture: %+v %v", page, err)
	}
	for _, a := range page.Artifacts {
		if a.SessionID != session || a.TurnID != turn || a.EnvironmentID != environment || !a.CreatedAt.Equal(completed.CompletedAt) {
			t.Fatalf("publication metadata: %+v", a)
		}
		foreign := uuid.NewString()
		if _, err := s.GetSessionArtifact(t.Context(), foreign, session, a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign metadata: %v", err)
		}
		if _, err := s.GetSessionArtifact(t.Context(), tenant, uuid.NewString(), a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("wrong session metadata: %v", err)
		}
		if err := s.DeleteSessionArtifact(t.Context(), foreign, session, a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign delete: %v", err)
		}
		if err := s.ReadSessionArtifact(t.Context(), foreign, session, a.ID, func(SessionArtifact, io.Reader) error {
			t.Error("foreign read reached content")
			return nil
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign read: %v", err)
		}
	}
	if _, err := s.ListSessionArtifacts(t.Context(), uuid.NewString(), session, "", "", 100, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign list: %v", err)
	}
	if empty, err := s.ListSessionArtifacts(t.Context(), tenant, session, uuid.NewString(), "", 100, false); err != nil || len(empty.Artifacts) != 0 {
		t.Fatalf("environment filter: %+v %v", empty, err)
	}
	// A later completed Turn publishes another immutable version of the same path.
	next := submitMessage(t, s, tenant, session, "version-two")
	transition(t, s, tenant, session, next.TurnID, TurnQueued, TurnInProgress)
	if err := s.StageTurnArtifacts(t.Context(), tenant, session, next.TurnID, environment, bytes.NewReader(artifactArchive(t, map[string][]byte{"outputs/a.bin": []byte("new")}))); err != nil {
		t.Fatal(err)
	}
	transition(t, s, tenant, session, next.TurnID, TurnInProgress, TurnCompleted)
	all, err := s.ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true)
	if err != nil || len(all.Artifacts) != 3 {
		t.Fatal(all, err)
	}
	for _, asc := range []bool{true, false} {
		var got []SessionArtifact
		cursor := ""
		for {
			part, err := s.ListSessionArtifacts(t.Context(), tenant, session, environment, cursor, 1, asc)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, part.Artifacts...)
			if part.NextCursor == "" {
				break
			}
			if len(got) > 3 {
				t.Fatal("pagination repeated artifacts")
			}
			cursor = part.NextCursor
		}
		want := append([]SessionArtifact(nil), all.Artifacts...)
		if !asc {
			want[0], want[2] = want[2], want[0]
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ordered pages differ: %+v %+v", got, want)
		}
	}
	// Expiration is a controlled fixture; stored reads must not touch Runtime.
	if _, err := pool.Exec(t.Context(), "UPDATE environments SET status='expired' WHERE id=$1", environment); err != nil {
		t.Fatal(err)
	}
	for _, a := range page.Artifacts {
		if err := New(pool).ReadSessionArtifact(t.Context(), tenant, session, a.ID, func(meta SessionArtifact, r io.Reader) error {
			if err := s.DeleteSessionArtifact(t.Context(), tenant, session, a.ID); err != nil {
				return err
			}
			body, err := io.ReadAll(r)
			want := data
			if a.Path == "/workspace/outputs/nested/empty" {
				want = nil
			}
			if meta != a || !bytes.Equal(body, want) {
				t.Error("expired/deleted artifact damaged admitted snapshot")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSessionArtifact(t.Context(), tenant, session, a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted metadata retained: %v", err)
		}
	}
	if err := s.DeleteSession(t.Context(), tenant, session); err != nil {
		t.Fatal(err)
	}
	if count := sourceObjectCount(t, pool); count != before {
		t.Fatalf("objects leaked: %d -> %d", before, count)
	}
}

type artifactReadError struct{}

func (artifactReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSessionArtifactsRejectIncompleteAndUnownedCapture(t *testing.T) {
	for _, kind := range []string{"openai_hosted", "self_hosted"} {
		t.Run(kind, func(t *testing.T) { testSessionArtifactsRejectIncompleteAndUnownedCapture(t, kind) })
	}
}

func testSessionArtifactsRejectIncompleteAndUnownedCapture(t *testing.T, kind string) {
	s, pool := testStore(t)
	tenant, session, environment, turn := artifactTurn(t, s, kind)
	before := sourceObjectCount(t, pool)
	valid := artifactArchive(t, map[string][]byte{"outputs/a": []byte("data")})
	for name, body := range map[string]io.Reader{
		"transport-failure-after-valid-tar": io.MultiReader(bytes.NewReader(valid), artifactReadError{}),
		"truncated-body":                    bytes.NewReader(valid[:513]),
		"trailing-data":                     io.MultiReader(bytes.NewReader(valid), bytes.NewReader([]byte("not archive padding"))),
		"traversal":                         bytes.NewReader(artifactArchive(t, map[string][]byte{"outputs/../secret": []byte("no")})),
		"private-root":                      bytes.NewReader(artifactArchive(t, map[string][]byte{"secrets/key": []byte("no")})),
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, body); err == nil {
				t.Fatal("invalid capture accepted")
			}
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("rollback leaked objects: %d -> %d", before, count)
			}
		})
	}
	for _, ids := range [][4]string{{uuid.NewString(), session, turn, environment}, {tenant, session, turn, uuid.NewString()}} {
		if err := s.StageTurnArtifacts(t.Context(), ids[0], ids[1], ids[2], ids[3], artifactReadError{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unauthorized capture reached reader: %v", err)
		}
	}
}

func TestSessionArtifactsDiscardTerminalPrivateCapture(t *testing.T) {
	for _, status := range []string{TurnFailed, TurnCancelled} {
		t.Run(status, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session, environment, turn := artifactTurn(t, s, "openai_hosted")
			before := sourceObjectCount(t, pool)
			body := artifactArchive(t, map[string][]byte{"outputs/a": []byte("private")})
			if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(body)); err != nil {
				t.Fatal(err)
			}
			transition(t, s, tenant, session, turn, TurnInProgress, status)
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("terminal capture leaked objects: %d -> %d", before, count)
			}
			if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(body)); !errors.Is(err, ErrTurnConflict) {
				t.Fatalf("late capture accepted: %v", err)
			}
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("late capture leaked objects: %d -> %d", before, count)
			}
		})
	}
}

func TestSessionArtifactTransferDoesNotBlockDeletionOrCancellation(t *testing.T) {
	for _, operation := range []string{"delete", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session, environment, turn := artifactTurn(t, s, "openai_hosted")
			before := sourceObjectCount(t, pool)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			result := make(chan error, 1)
			go func() { result <- s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, reader) }()
			// A complete TAR arrives, but transport has not acknowledged success yet.
			if _, err := writer.Write(artifactArchive(t, map[string][]byte{"outputs/a": []byte("partial")})); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			want := ErrNotFound
			if operation == "delete" {
				// The idle-only decision itself is not blocked by the transfer.
				if err := s.DeleteSession(ctx, tenant, session); !errors.Is(err, ErrSessionNotIdle) {
					t.Fatalf("transfer blocked or bypassed the deletion rule: %v", err)
				}
				if err := s.commitLegacyDeletion(ctx, tenant, session); err != nil {
					t.Fatalf("transfer blocked deletion: %v", err)
				}
			} else {
				if _, err := s.RequestCancel(ctx, tenant, session, "cancel-capture"); err != nil {
					t.Fatalf("transfer blocked cancellation: %v", err)
				}
				want = ErrTurnConflict
			}
			writer.Close()
			if err := <-result; !errors.Is(err, want) {
				t.Fatalf("late publication after %s: %v", operation, err)
			}
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("late capture leaked objects: %d -> %d", before, count)
			}
		})
	}
}

// startArtifactTurn admits another message and starts its Turn.
func startArtifactTurn(t *testing.T, s *Store, tenant, session, key string) string {
	t.Helper()
	input := submitMessage(t, s, tenant, session, key)
	transition(t, s, tenant, session, input.TurnID, TurnQueued, TurnInProgress)
	return input.TurnID
}

// stageArtifactOutputs privately captures one complete outputs tree for a Turn.
func stageArtifactOutputs(t *testing.T, s *Store, tenant, session, environment, turn string, files map[string]string) {
	t.Helper()
	archive := make(map[string][]byte, len(files))
	for name, body := range files {
		archive["outputs/"+name] = []byte(body)
	}
	if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(artifactArchive(t, archive))); err != nil {
		t.Fatal(err)
	}
}

// publishedByTurn returns the Artifacts one Turn published, keyed by outputs-relative path.
func publishedByTurn(t *testing.T, s *Store, tenant, session, turn string) map[string]SessionArtifact {
	t.Helper()
	page, err := s.ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true)
	if err != nil || page.NextCursor != "" {
		t.Fatalf("list: %+v %v", page, err)
	}
	got := make(map[string]SessionArtifact)
	for _, artifact := range page.Artifacts {
		if artifact.TurnID == turn {
			got[strings.TrimPrefix(artifact.Path, "/workspace/outputs/")] = artifact
		}
	}
	return got
}

func artifactBytes(t *testing.T, s *Store, tenant, session, id string) string {
	t.Helper()
	var body []byte
	if err := s.ReadSessionArtifact(t.Context(), tenant, session, id, func(_ SessionArtifact, r io.Reader) error {
		var err error
		body, err = io.ReadAll(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func publishedPaths(published map[string]SessionArtifact) []string {
	paths := make([]string, 0, len(published))
	for path := range published {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// Later Turns publish a path only when it is new, its bytes differ from the
// newest remaining Artifact for that path, or no Artifact remains for it (HE-52).
func TestSessionArtifactsRepublishOnlyNewChangedOrDeletedPaths(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, environment, first := artifactTurn(t, s, "openai_hosted")
	before := sourceObjectCount(t, pool)
	turnNumber := 1
	run := func(files map[string]string, want ...string) map[string]SessionArtifact {
		t.Helper()
		turn := first
		if turnNumber > 1 {
			turn = startArtifactTurn(t, s, tenant, session, fmt.Sprintf("artifact-turn-%d", turnNumber))
		}
		turnNumber++
		stageArtifactOutputs(t, s, tenant, session, environment, turn, files)
		transition(t, s, tenant, session, turn, TurnInProgress, TurnCompleted)
		published := publishedByTurn(t, s, tenant, session, turn)
		sort.Strings(want)
		if got := publishedPaths(published); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("Turn %d published %v, want %v", turnNumber-1, got, want)
		}
		for path, artifact := range published {
			if body := artifactBytes(t, s, tenant, session, artifact.ID); body != files[path] {
				t.Fatalf("Turn %d %s bytes = %q, want %q", turnNumber-1, path, body, files[path])
			}
		}
		return published
	}
	unchanged := func(artifacts ...SessionArtifact) {
		t.Helper()
		for _, artifact := range artifacts {
			if got, err := s.GetSessionArtifact(t.Context(), tenant, session, artifact.ID); err != nil || got != artifact {
				t.Fatalf("existing Artifact changed: %+v -> %+v %v", artifact, got, err)
			}
		}
	}
	objects := func() {
		t.Helper()
		var rows int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM session_artifacts WHERE session_id = $1", session).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if count := sourceObjectCount(t, pool); count != before+rows {
			t.Fatalf("unpublished captures kept private objects: %d objects for %d Artifacts", count-before, rows)
		}
	}

	// The first Turn behaves as before: every regular output is published.
	outputs := map[string]string{"a.txt": "alpha", "sub/b.txt": "bravo", "empty.txt": ""}
	one := run(outputs, "a.txt", "sub/b.txt", "empty.txt")
	if err := s.DeleteSessionArtifact(t.Context(), tenant, session, one["a.txt"].ID); err != nil {
		t.Fatal(err)
	}
	// New c.txt and deleted-then-unchanged a.txt; unchanged paths keep their IDs.
	outputs["c.txt"] = "charlie"
	two := run(outputs, "a.txt", "c.txt")
	unchanged(one["sub/b.txt"], one["empty.txt"])
	objects()
	// Changed bytes publish a new version and leave the earlier one intact.
	outputs["sub/b.txt"] = "bravo-v2"
	three := run(outputs, "sub/b.txt")
	unchanged(one["sub/b.txt"], one["empty.txt"], two["a.txt"], two["c.txt"])
	if body := artifactBytes(t, s, tenant, session, one["sub/b.txt"].ID); body != "bravo" {
		t.Fatalf("earlier version changed: %q", body)
	}
	// Comparison uses the newest version, not any earlier one with equal bytes.
	outputs["sub/b.txt"] = "bravo"
	four := run(outputs, "sub/b.txt")
	// Entirely unchanged outputs, and a removed workspace file, publish nothing.
	delete(outputs, "c.txt")
	run(outputs)
	unchanged(one["sub/b.txt"], one["empty.txt"], two["a.txt"], two["c.txt"], three["sub/b.txt"], four["sub/b.txt"])
	objects()
	// Deletion leaves no tombstone: the newest remaining version is the comparison base.
	if err := s.DeleteSessionArtifact(t.Context(), tenant, session, four["sub/b.txt"].ID); err != nil {
		t.Fatal(err)
	}
	outputs["sub/b.txt"] = "bravo-v2"
	run(outputs)
	outputs["sub/b.txt"] = "bravo"
	run(outputs, "sub/b.txt")

	// A deletion committed after private capture but before completion is seen
	// by the completion transaction, so the same Turn republishes the path.
	turn := startArtifactTurn(t, s, tenant, session, "artifact-turn-delete-during-capture")
	stageArtifactOutputs(t, s, tenant, session, environment, turn, outputs)
	if err := s.DeleteSessionArtifact(t.Context(), tenant, session, two["a.txt"].ID); err != nil {
		t.Fatal(err)
	}
	transition(t, s, tenant, session, turn, TurnInProgress, TurnCompleted)
	if got := publishedPaths(publishedByTurn(t, s, tenant, session, turn)); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Fatalf("deletion during capture: published %v", got)
	}
	objects()

	// Another Session in the same tenant never compares against these Artifacts.
	other, err := s.CreateSession(t.Context(), tenant, environmentInput("artifact-other", "openai_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	otherEnvironment, err := s.GetSessionEnvironment(t.Context(), tenant, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherTurn := startArtifactTurn(t, s, tenant, other.ID, "artifact-other-turn")
	stageArtifactOutputs(t, s, tenant, other.ID, otherEnvironment.ID, otherTurn, outputs)
	transition(t, s, tenant, other.ID, otherTurn, TurnInProgress, TurnCompleted)
	if got := publishedPaths(publishedByTurn(t, s, tenant, other.ID, otherTurn)); !reflect.DeepEqual(got, []string{"a.txt", "empty.txt", "sub/b.txt"}) {
		t.Fatalf("other Session first Turn published %v", got)
	}
	for _, id := range []string{session, other.ID} {
		if err := s.DeleteSession(t.Context(), tenant, id); err != nil {
			t.Fatal(err)
		}
	}
	if count := sourceObjectCount(t, pool); count != before {
		t.Fatalf("objects leaked: %d -> %d", before, count)
	}
}

// Publication time can come from the Runtime's reported completion and invert
// the order of Turns; the newest version for a path still follows Turn order.
func TestSessionArtifactsNewestVersionFollowsTurnOrder(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	created, err := s.CreateSession(t.Context(), tenant, environmentInput("artifact-order", "openai_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	session := created.ID
	env, err := s.GetSessionEnvironment(t.Context(), tenant, session)
	if err != nil {
		t.Fatal(err)
	}
	// Turn 1 reports a native completion one hour ahead, so its Artifact is
	// published later than every following Turn's.
	first := submitMessage(t, s, tenant, session, "artifact-order-1")
	transition(t, s, tenant, session, first.TurnID, TurnQueued, TurnInProgress)
	stageArtifactOutputs(t, s, tenant, session, env.ID, first.TurnID, map[string]string{"b.txt": "bravo"})
	future := time.Now().Add(time.Hour).UnixMilli()
	if _, err := s.CompleteExecution(t.Context(), tenant, session, first.TurnID, TurnCompleted, json.RawMessage(fmt.Sprintf(`{"done":{"source_completed_at_ms":%d}}`, future)), "", first.Sequence); err != nil {
		t.Fatal(err)
	}
	one := publishedByTurn(t, s, tenant, session, first.TurnID)["b.txt"]
	run := func(key, body string) map[string]SessionArtifact {
		t.Helper()
		turn := startArtifactTurn(t, s, tenant, session, key)
		stageArtifactOutputs(t, s, tenant, session, env.ID, turn, map[string]string{"b.txt": body})
		transition(t, s, tenant, session, turn, TurnInProgress, TurnCompleted)
		return publishedByTurn(t, s, tenant, session, turn)
	}
	two := run("artifact-order-2", "bravo-v2")["b.txt"]
	if two.ID == "" || !two.CreatedAt.Before(one.CreatedAt) {
		t.Fatalf("fixture did not invert publication time: %+v %+v", one, two)
	}
	// Turn 2's version is the newest although Turn 1 was published later.
	if got := run("artifact-order-3", "bravo-v2"); len(got) != 0 {
		t.Fatalf("unchanged bytes of the newest Turn republished: %+v", got)
	}
	if got := publishedPaths(run("artifact-order-4", "bravo")); strings.Join(got, ",") != "b.txt" {
		t.Fatalf("bytes of an older Turn's version were not republished: %v", got)
	}
}

// A deletion that holds the Session lock while Turn completion waits for it is
// seen by the completion transaction, which then republishes the path.
func TestSessionArtifactsCompletionWaitsForConcurrentDeletion(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, environment, first := artifactTurn(t, s, "openai_hosted")
	before := sourceObjectCount(t, pool)
	stageArtifactOutputs(t, s, tenant, session, environment, first, map[string]string{"a.txt": "alpha"})
	transition(t, s, tenant, session, first, TurnInProgress, TurnCompleted)
	newest := publishedByTurn(t, s, tenant, session, first)["a.txt"]
	turn := startArtifactTurn(t, s, tenant, session, "artifact-concurrent-delete")
	stageArtifactOutputs(t, s, tenant, session, environment, turn, map[string]string{"a.txt": "alpha"})

	// Delete exactly as DeleteSessionArtifact does, but keep the transaction open.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	lookup, err := artifactLookup(tenant, session, newest.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := s.queries.WithTx(tx)
	if _, err := q.LockSession(t.Context(), sqlc.LockSessionParams{TenantID: lookup.TenantID, ID: lookup.SessionID}); err != nil {
		t.Fatal(err)
	}
	oid, err := q.DeleteSessionArtifact(t.Context(), sqlc.DeleteSessionArtifactParams(lookup))
	if err != nil {
		t.Fatal(err)
	}
	objects := tx.LargeObjects()
	if err := objects.Unlink(t.Context(), oid.Uint32); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.TransitionTurn(t.Context(), tenant, session, turn, TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnCompleted})
		done <- err
	}()
	// Completion must be blocked on the Session lock before the deletion commits.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("completion did not wait for the Session lock: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("completion never waited for the Session lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status, err := s.GetTurn(t.Context(), tenant, session, turn); err != nil || status.Status != TurnInProgress {
		t.Fatalf("Turn settled while the deletion held the lock: %+v %v", status, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	published := publishedByTurn(t, s, tenant, session, turn)
	if got := publishedPaths(published); strings.Join(got, ",") != "a.txt" || artifactBytes(t, s, tenant, session, published["a.txt"].ID) != "alpha" {
		t.Fatalf("concurrent deletion was not republished: %v", got)
	}
	if _, err := s.GetSessionArtifact(t.Context(), tenant, session, newest.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted Artifact remains: %v", err)
	}
	if count := sourceObjectCount(t, pool); count != before+1 {
		t.Fatalf("private objects: %d -> %d, want one published Artifact", before, count)
	}
}
