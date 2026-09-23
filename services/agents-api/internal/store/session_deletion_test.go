package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// commitLegacyDeletion reproduces the deletion transaction of releases before
// the idle-only rule: it requested cancellation of pending work and committed
// the marker together. Upgraded databases can retain such markers, so hidden
// work must still settle; tests use this to reach that state.
func (s *Store) commitLegacyDeletion(ctx context.Context, tenantID, sessionID string) error {
	return s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		if err := cancelSessionWork(ctx, q, session); err != nil {
			return err
		}
		if err := q.DeleteSessionArtifacts(ctx, session); err != nil {
			return err
		}
		if err := q.ReleaseUnallocatedRuntimePlacement(ctx, session); err != nil {
			return err
		}
		return q.MarkSessionDeleted(ctx, session)
	})
}

// sessionDeletedAt reads the internal marker, which public reads never expose.
func sessionDeletedAt(t *testing.T, pool *pgxpool.Pool, session string) pgtype.Timestamptz {
	t.Helper()
	var deleted pgtype.Timestamptz
	if err := pool.QueryRow(t.Context(), "SELECT deleted_at FROM sessions WHERE id=$1", session).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	return deleted
}

func TestSessionDeletionWaitsForSettledTurnAndRejectsAdmission(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	tenant := uuid.NewString()
	for _, status := range []string{TurnQueued, TurnInProgress, TurnCompleted, TurnFailed} {
		t.Run(status, func(t *testing.T) {
			input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: status}
			session, err := s.CreateSession(ctx, tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := s.SubmitMessage(ctx, tenant, session.ID, "input", json.RawMessage(`{"text":"retained"}`))
			if err != nil {
				t.Fatal(err)
			}
			if status != TurnQueued {
				_, err = s.TransitionTurn(ctx, tenant, session.ID, receipt.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress})
				if err != nil {
					t.Fatal(err)
				}
			}
			if status == TurnCompleted || status == TurnFailed {
				if _, err = s.CompleteExecution(ctx, tenant, session.ID, receipt.TurnID, status, nil, "", receipt.Sequence); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.DeleteSession(ctx, uuid.NewString(), session.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if status == TurnQueued || status == TurnInProgress {
				// Deletion leaves active work untouched: no cancellation, marker or event.
				cursor, err := s.SessionEventCursor(ctx, tenant, session.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.DeleteSession(ctx, tenant, session.ID); !errors.Is(err, ErrSessionNotIdle) {
					t.Fatal("active Session deleted", err)
				}
				turn, err := s.GetTurn(ctx, tenant, session.ID, receipt.TurnID)
				if err != nil || turn.Status != status || !turn.CancelRequestedAt.IsZero() {
					t.Fatal("rejected deletion changed the Turn", turn, err)
				}
				if after, err := s.SessionEventCursor(ctx, tenant, session.ID); err != nil || after != cursor {
					t.Fatal("rejected deletion recorded an event", after, cursor, err)
				}
				if sessionDeletedAt(t, pool, session.ID).Valid {
					t.Fatal("rejected deletion committed a marker")
				}
				// Callers cancel first. A queued Turn cancels at once; a running
				// Turn stays active until execution settles its cancellation.
				if _, err := s.RequestCancel(ctx, tenant, session.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				if status == TurnInProgress {
					if err := s.DeleteSession(ctx, tenant, session.ID); !errors.Is(err, ErrSessionNotIdle) {
						t.Fatal("cancelling Session deleted", err)
					}
					if _, err := s.CompleteExecution(ctx, tenant, session.ID, receipt.TurnID, TurnCancelled, nil, "", receipt.Sequence); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.DeleteSession(ctx, tenant, session.ID); err != nil {
				t.Fatal(err)
			}
			marker := sessionDeletedAt(t, pool, session.ID)
			fresh := New(pool)
			// The owner's repeated deletion confirms again without another write.
			for _, repeat := range []*Store{s, fresh} {
				if err := repeat.DeleteSession(ctx, tenant, session.ID); err != nil {
					t.Fatal("repeated deletion", err)
				}
				if err := repeat.DeleteSession(ctx, uuid.NewString(), session.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("foreign deleted Session", err)
				}
			}
			if again := sessionDeletedAt(t, pool, session.ID); again != marker {
				t.Fatal("repeated deletion rewrote the marker", marker, again)
			}
			if _, err := fresh.GetSession(ctx, tenant, session.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := fresh.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal(err)
			}
			if _, err := fresh.CreateSessionStream(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal(err)
			}
			if _, err := fresh.SubmitMessage(ctx, tenant, session.ID, "input", json.RawMessage(`{"text":"retained"}`)); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := fresh.RequestCancel(ctx, tenant, session.ID, "late-cancel"); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := fresh.ListItems(ctx, tenant, session.ID, "", 20, true); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			turn, err := fresh.GetTurn(ctx, tenant, session.ID, receipt.TurnID)
			if err != nil {
				t.Fatal(err)
			}
			want := status
			if status == TurnQueued || status == TurnInProgress {
				want = TurnCancelled
			}
			if turn.Status != want {
				t.Fatal(turn)
			}
			if _, err := fresh.TransitionTurn(ctx, tenant, session.ID, receipt.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress}); !errors.Is(err, ErrTurnConflict) {
				t.Fatal(err)
			}
			inputs, err := fresh.ListTurnInputs(ctx, tenant, session.ID, receipt.TurnID, 0, 20)
			if err != nil || len(inputs) == 0 || inputs[0].Sequence != receipt.Sequence {
				t.Fatal(inputs, err)
			}
			if _, err := fresh.SessionEventCursor(ctx, tenant, session.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := fresh.ListSessionEvents(ctx, tenant, session.ID, 0); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionDeletionSerializesAdmissionBeforeRetryLookup(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "creation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestCancel(ctx, tenant, session.ID, "existing"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.RequestCancel(ctx, tenant, session.ID, "existing"); done <- err }()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrNotFound) {
		t.Fatal("retry admitted after deletion", err)
	}
}

// afterSessionLock runs once, inside the traced transaction, right after it
// acquires the Session row lock.
type afterSessionLock struct {
	once sync.Once
	run  func()
}

type sessionLockQuery struct{}

func (a *afterSessionLock) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, sessionLockQuery{}, strings.HasPrefix(data.SQL, "-- name: LockSession "))
}

func (a *afterSessionLock) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if locked, _ := ctx.Value(sessionLockQuery{}).(bool); locked && data.Err == nil {
		a.once.Do(a.run)
	}
}

// awaitSessionLockWaiter waits until another connection blocks on a Session lock.
func awaitSessionLockWaiter(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '-- name: LockSession %'`).Scan(&waiting)
		if err != nil {
			t.Error(err)
			return
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Error("competing operation never waited for the Session lock")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSessionDeletionRacesAdmissionUnderSessionLock runs the production deletion
// and admission paths against one real PostgreSQL row lock. Whichever commits
// first decides: admitted work makes deletion conflict without mutation, and a
// committed deletion makes admission not found. Both never succeed.
func TestSessionDeletionRacesAdmissionUnderSessionLock(t *testing.T) {
	type admission struct {
		name  string
		setup func(t *testing.T, s *Store) (string, string)
		admit func(ctx context.Context, s *Store, tenant, session string) error
	}
	admissions := []admission{
		{"turn", func(t *testing.T, s *Store) (string, string) {
			tenant, session := newTurnSession(t, s)
			return tenant, session.ID
		}, func(ctx context.Context, s *Store, tenant, session string) error {
			_, err := s.SubmitMessage(ctx, tenant, session, "racing", messagePayload)
			return err
		}},
		{"environment_input", func(t *testing.T, s *Store) (string, string) {
			tenant, session := environmentInputSession(t, s)
			return tenant, session.ID
		}, func(ctx context.Context, s *Store, tenant, session string) error {
			_, err := s.ReserveEnvironmentInput(ctx, tenant, session, "racing", []Input{messageInput("racing")})
			return err
		}},
	}
	// An isolated database keeps lock-wait observation independent of other tests.
	plain, pool := newManagedTestStore(t)
	traced := func(t *testing.T, run func()) *Store {
		cfg := pool.Config().Copy()
		cfg.ConnConfig.Tracer = &afterSessionLock{run: run}
		instrumented, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(instrumented.Close)
		return New(instrumented)
	}
	for _, kind := range admissions {
		t.Run(kind.name+"/admission-first", func(t *testing.T) {
			tenant, session := kind.setup(t, plain)
			deleted := make(chan error, 1)
			s := traced(t, func() {
				go func() { deleted <- plain.DeleteSession(context.Background(), tenant, session) }()
				awaitSessionLockWaiter(t, pool)
			})
			if err := kind.admit(t.Context(), s, tenant, session); err != nil {
				t.Fatal(err)
			}
			if err := <-deleted; !errors.Is(err, ErrSessionNotIdle) {
				t.Fatal("deletion ignored committed admission", err)
			}
			if sessionDeletedAt(t, pool, session).Valid {
				t.Fatal("rejected deletion committed a marker")
			}
			current, err := plain.GetSession(t.Context(), tenant, session)
			if err != nil {
				t.Fatal(err)
			}
			if kind.name == "turn" && (current.LastTurn == nil || current.LastTurn.Status != TurnQueued || !current.LastTurn.CancelRequestedAt.IsZero()) {
				t.Fatal("rejected deletion changed admitted work", current.LastTurn)
			}
			if kind.name == "environment_input" && !current.PendingInput {
				t.Fatal("rejected deletion settled pending input", current)
			}
		})
		t.Run(kind.name+"/deletion-first", func(t *testing.T) {
			tenant, session := kind.setup(t, plain)
			admitted := make(chan error, 1)
			s := traced(t, func() {
				go func() { admitted <- kind.admit(context.Background(), plain, tenant, session) }()
				awaitSessionLockWaiter(t, pool)
			})
			if err := s.DeleteSession(t.Context(), tenant, session); err != nil {
				t.Fatal(err)
			}
			if err := <-admitted; !errors.Is(err, ErrNotFound) {
				t.Fatal("admission after deletion", err)
			}
			var turns, reservations int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM turns WHERE session_id=$1),
				(SELECT count(*) FROM environment_input_reservations WHERE session_id=$1)`, session).Scan(&turns, &reservations); err != nil {
				t.Fatal(err)
			}
			if turns != 0 || reservations != 0 {
				t.Fatal("deleted Session admitted work", turns, reservations)
			}
		})
		t.Run(kind.name+"/concurrent", func(t *testing.T) {
			for range 8 {
				tenant, session := kind.setup(t, plain)
				other := New(pool)
				start := make(chan struct{})
				results := make(chan error, 2)
				go func() { <-start; results <- plain.DeleteSession(context.Background(), tenant, session) }()
				go func() { <-start; results <- kind.admit(context.Background(), other, tenant, session) }()
				close(start)
				first, second := <-results, <-results
				deleted := sessionDeletedAt(t, pool, session).Valid
				var active int
				if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM turns WHERE session_id=$1)
					+ (SELECT count(*) FROM environment_input_reservations WHERE session_id=$1 AND state='pending')`, session).Scan(&active); err != nil {
					t.Fatal(err)
				}
				// Exactly one side succeeds; the other reports the committed state.
				failures := 0
				for _, err := range []error{first, second} {
					if err != nil {
						failures++
						if !errors.Is(err, ErrSessionNotIdle) && !errors.Is(err, ErrNotFound) {
							t.Fatal(err)
						}
					}
				}
				if failures != 1 || deleted == (active > 0) {
					t.Fatal("deletion and admission both decided", first, second, deleted, active)
				}
			}
		})
	}
}
