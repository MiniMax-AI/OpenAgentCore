package sessionpg

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var errRollback = errors.New("roll back")

func pgID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// newEnvironment stores a fresh tenant's Session whose Environment has kind
// and status, and returns the tenant, Session and Environment IDs.
func newEnvironment(t *testing.T, pool *pgxpool.Pool, kind, status string) (pgtype.UUID, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	tenant, session, environment := uuid.New(), uuid.New(), uuid.New()
	exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration)
		VALUES ($1, $2, 'codex', 'key', 'hash', jsonb_build_object('environment', jsonb_build_object('type', $3::text)))`, session, tenant, kind)
	exec(t, pool, `INSERT INTO environments(id, session_id, status) VALUES ($1, $2, $3)`, environment, session, status)
	return pgID(tenant), pgID(session), pgID(environment)
}

func reserveInput(t *testing.T, pool *pgxpool.Pool, session pgtype.UUID) {
	t.Helper()
	exec(t, pool, `INSERT INTO environment_input_reservations(id, session_id, idempotency_key, batch, created_at, deadline)
		VALUES ($1, $2, 'input', '[{}]', clock_timestamp(), clock_timestamp() + interval '1 hour')`, uuid.New(), session)
}

func kinds(changes []sessions.SessionChange) []string {
	var types []string
	for _, change := range changes {
		types = append(types, change.Event.Type)
	}
	return types
}

func TestWithSessionLocksAppliesAndPrunes(t *testing.T) {
	pool := pgtest.Open(t)
	runner := pgunit.NewPool(pool)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "connected")
	change := sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.idle"}}

	err := WithSession(t.Context(), runner, pgID(uuid.New()), session, func(context.Context, *sqlc.Queries, sessions.LockedSession) error {
		t.Fatal("applied to another tenant's Session")
		return nil
	})
	if !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}

	err = WithSession(t.Context(), runner, tenant, session, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		if err := AppendChanges(ctx, q, session, change); err != nil {
			return err
		}
		return errRollback
	})
	if _, changes := journal(t, pool, session); !errors.Is(err, errRollback) || len(changes) != 0 {
		t.Fatalf("failed apply committed %d changes: %v", len(changes), err)
	}

	err = WithSession(t.Context(), runner, tenant, session, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		if locked.Deleted {
			t.Fatal("live Session locked as deleted")
		}
		for range sessions.RetainedChanges + 2 {
			if err := AppendChanges(ctx, q, session, change); err != nil {
				return err
			}
		}
		return nil
	})
	if _, changes := journal(t, pool, session); err != nil || len(changes) != sessions.RetainedChanges {
		t.Fatalf("retained %d changes: %v", len(changes), err)
	}

	exec(t, pool, `UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1`, session)
	err = WithSession(t.Context(), runner, tenant, session, func(_ context.Context, _ *sqlc.Queries, locked sessions.LockedSession) error {
		return locked.Public()
	})
	if !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("deleted Session: %v", err)
	}
}

// WithSession holds the Session lock until its transaction ends: another
// transaction cannot lock the Session meanwhile, and can once it has ended.
func TestWithSessionHoldsTheSessionLock(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "connected")
	lock := func(ctx context.Context) error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
				return err
			}
			_, err := LockSession(ctx, sqlc.New(tx), tenant, session)
			return err
		})
	}

	err := WithSession(t.Context(), pgunit.NewPool(pool), tenant, session, func(ctx context.Context, _ *sqlc.Queries, _ sessions.LockedSession) error {
		return lock(ctx)
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("locked a held Session: %v", err)
	}
	if err := lock(t.Context()); err != nil {
		t.Fatalf("lock after the holder ended: %v", err)
	}
}

// A binding reads and writes only its tenant's Session's own Environment:
// another tenant, or another Session's Environment, is an error and changes no
// row.
func TestBindingScopesTheEnvironmentToItsSession(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, environment := newEnvironment(t, pool, "openai_hosted", "connected")
	_, _, foreign := newEnvironment(t, pool, "openai_hosted", "connected")
	stranger := pgID(uuid.New())
	inTx(t, pool, func(q *sqlc.Queries) error {
		if _, err := BindSession(q, stranger, session).LoadEnvironment(t.Context()); !errors.Is(err, sessions.ErrNotFound) {
			t.Errorf("other tenant loaded the Environment: %v", err)
		}
		for name, test := range map[string]struct {
			bound       *SessionTx
			environment pgtype.UUID
		}{
			"other Session's Environment": {BindSession(q, tenant, session), foreign},
			"other tenant":                {BindSession(q, stranger, session), environment},
		} {
			id := uuid.UUID(test.environment.Bytes).String()
			if _, err := test.bound.RecordEnvironmentFailure(t.Context(), id, "reason", nil); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("%s failed: %v", name, err)
			}
			if err := test.bound.ExpireEnvironment(t.Context(), id); !errors.Is(err, sessions.ErrNotFound) {
				t.Errorf("%s expired: %v", name, err)
			}
		}
		return nil
	})
	for _, id := range []pgtype.UUID{environment, foreign} {
		var status string
		if err := pool.QueryRow(t.Context(), `SELECT status FROM environments WHERE id = $1`, id).Scan(&status); err != nil || status != "connected" {
			t.Fatalf("Environment %s: %v", status, err)
		}
	}
}

type environmentState struct {
	environment, turn, input string
	events                   int
}

func readEnvironmentState(t *testing.T, pool *pgxpool.Pool, session pgtype.UUID) environmentState {
	t.Helper()
	var state environmentState
	if err := pool.QueryRow(t.Context(), `SELECT e.status, COALESCE((SELECT status FROM turns WHERE session_id = e.session_id), ''),
		(SELECT state FROM environment_input_reservations WHERE session_id = e.session_id),
		(SELECT count(*) FROM session_events WHERE session_id = e.session_id)
		FROM environments e WHERE e.session_id = $1`, session).Scan(&state.environment, &state.turn, &state.input, &state.events); err != nil {
		t.Fatal(err)
	}
	return state
}

// The binding writes only through its caller's transaction: rolling that
// transaction back reverts every participant's writes, and committing it keeps
// them with the events in the procedure's order.
func TestFailEnvironmentThroughTheBindingCommitsWithItsTransaction(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, _ := newEnvironment(t, pool, "openai_hosted", "connected")
	exec(t, pool, `INSERT INTO turns(id, session_id) VALUES ($1, $2)`, uuid.New(), session)
	reserveInput(t, pool, session)
	const reason = "Failed to provision environment"
	fail := func(tx pgx.Tx) error {
		bound := BindSession(sqlc.New(tx), tenant, session)
		environment, err := bound.LoadEnvironment(t.Context())
		if err != nil {
			return err
		}
		return sessions.FailEnvironment(t.Context(), bound, environment, reason, &sessions.ProvisioningFailureDetail{})
	}

	err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		if err := fail(tx); err != nil {
			return err
		}
		return errRollback
	})
	if got := readEnvironmentState(t, pool, session); !errors.Is(err, errRollback) || got != (environmentState{"connected", sessions.TurnQueued, sessions.EnvironmentInputPending, 0}) {
		t.Fatalf("rolled back to %+v: %v", got, err)
	}

	if err := pgx.BeginFunc(t.Context(), pool, fail); err != nil {
		t.Fatal(err)
	}
	if got := readEnvironmentState(t, pool, session); got != (environmentState{"failed", sessions.TurnCancelled, sessions.EnvironmentInputFailed, 5}) {
		t.Fatalf("committed %+v", got)
	}
	var failedAt time.Time
	if err := pool.QueryRow(t.Context(), `SELECT failed_at FROM environments WHERE session_id = $1`, session).Scan(&failedAt); err != nil {
		t.Fatal(err)
	}
	_, changes := journal(t, pool, session)
	want := []string{"agent.session.environment.failed", "agent.session.turn.cancelled", "agent.session.idle", "error", "agent.session.failed"}
	failed := changes[len(changes)-1]
	if !reflect.DeepEqual(kinds(changes), want) || !failed.Settled || !failed.EnvironmentFailure.FailedAt.Equal(failedAt) ||
		failed.EnvironmentInputActivity == nil || failed.EnvironmentInputActivity.Status != "failed" {
		t.Fatalf("journal %q, failed %+v", kinds(changes), failed)
	}
}

// Expired compute expires a live Environment, fails its pending input and
// reports the input activity that changed.
func TestTerminateEnvironmentThroughTheBindingExpires(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "connected")
	reserveInput(t, pool, session)
	inTx(t, pool, func(q *sqlc.Queries) error {
		return sessions.TerminateEnvironment(t.Context(), BindSession(q, tenant, session), true, "reason", nil)
	})
	if got := readEnvironmentState(t, pool, session); got != (environmentState{"expired", "", sessions.EnvironmentInputFailed, 1}) {
		t.Fatalf("terminated %+v", got)
	}
	_, changes := journal(t, pool, session)
	if changes[0].Event.Type != "agent.session.failed" || !changes[0].Settled || changes[0].EnvironmentInputActivity.Failure != "environment_unavailable" || changes[0].EnvironmentFailure != nil {
		t.Fatalf("change %+v", changes[0])
	}
}

func TestCreateEnvironmentDeviceBindsOneDevice(t *testing.T) {
	pool := pgtest.Open(t)
	hash := strings.Repeat("a", 64)
	create := func(tenant, session, environment pgtype.UUID, device uuid.UUID, commit bool) error {
		dedicated := sessions.ExecutionDevice{ID: device.String(), Name: "runtime", EnvironmentID: uuid.UUID(environment.Bytes).String()}
		return pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
			if err := sessions.CreateEnvironmentDevice(t.Context(), BindSession(sqlc.New(tx), tenant, session), dedicated, hash); err != nil || commit {
				return err
			}
			return errRollback
		})
	}
	bound := func(session pgtype.UUID) []uuid.UUID {
		rows, err := pool.Query(t.Context(), `SELECT device_id FROM session_devices WHERE session_id = $1`, session)
		if err != nil {
			t.Fatal(err)
		}
		devices, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			t.Fatal(err)
		}
		return devices
	}

	tenant, session, environment := newEnvironment(t, pool, "openai_hosted", "pending")
	if err := create(tenant, session, environment, uuid.New(), false); !errors.Is(err, errRollback) || len(bound(session)) != 0 {
		t.Fatalf("rolled back creation bound %v: %v", bound(session), err)
	}
	if err := create(pgID(uuid.New()), session, environment, uuid.New(), true); !errors.Is(err, sessions.ErrDeviceBindingConflict) || len(bound(session)) != 0 {
		t.Fatalf("other tenant bound %v: %v", bound(session), err)
	}
	device := uuid.New()
	if err := create(tenant, session, environment, device, true); err != nil || !reflect.DeepEqual(bound(session), []uuid.UUID{device}) {
		t.Fatalf("bound %v: %v", bound(session), err)
	}
	if err := create(tenant, session, environment, uuid.New(), true); !errors.Is(err, sessions.ErrDeviceBindingConflict) {
		t.Fatalf("second device: %v", err)
	}
	tenant, session, environment = newEnvironment(t, pool, "self_hosted", "pending")
	if err := create(tenant, session, environment, uuid.New(), true); !errors.Is(err, sessions.ErrDeviceBindingConflict) {
		t.Fatalf("self-hosted device: %v", err)
	}
}
