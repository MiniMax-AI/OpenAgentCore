package sessionpg

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// leasedInputs opens a database of its own and returns its pool, the Session
// store and service on the pool, and the execution operations on its lease.
func leasedInputs(t *testing.T) (*pgxpool.Pool, *Store, *sessions.Service, *sessions.ExecutionOperations) {
	t.Helper()
	pool := pgtest.OpenIsolated(t, nil)
	store, service := stagingService(t, pool)
	operations, _ := sessionExecution(t, pool)
	return pool, store, service, operations
}

// terminateLeaseOwner ends the backend that holds the execution lease of
// pool's database.
func terminateLeaseOwner(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var killed bool
	err := pool.QueryRow(t.Context(), `SELECT pg_terminate_backend(pid, 1000) FROM pg_locks WHERE locktype='advisory' AND granted AND objsubid=1
		AND classid::bigint * 4294967296 + objid::bigint = 706172736172
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&killed)
	if err != nil || !killed {
		t.Fatal("terminate execution lease owner", killed, err)
	}
}

func reservationState(t *testing.T, store *Store, tenant, session pgtype.UUID, reservation string) string {
	t.Helper()
	got, err := store.GetEnvironmentInputReservation(t.Context(), text(tenant), text(session), reservation)
	if err != nil {
		t.Fatal(err)
	}
	return got.State
}

// lockSession holds the Session's lock in an open transaction and returns the
// holder's backend PID; the transaction rolls back when the test ends unless
// release commits it first.
func lockSession(ctx context.Context, t *testing.T, pool *pgxpool.Pool, session pgtype.UUID) (int32, func(sql string, args ...any)) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	var pid int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM sessions WHERE id=$1 FOR UPDATE", session).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid, func(sql string, args ...any) {
		t.Helper()
		if sql != "" {
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// awaitBlocked waits until a backend waits on holder's lock and returns its
// PID.
func awaitBlocked(ctx context.Context, t *testing.T, pool *pgxpool.Pool, holder int32) int32 {
	t.Helper()
	for ctx.Err() == nil {
		var blocked int32
		if err := pool.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1", holder).Scan(&blocked); err == nil {
			return blocked
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("transaction did not wait for the Session lock")
	return 0
}

// Direct input waits behind a pending reservation; promotion admits it once,
// and every retry, after a new execution owner and pool too, replays that
// admission.
func TestEnvironmentInputReservationPromotionAndDirectRetries(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	store, service := stagingService(t, pool)
	operations, lease := sessionExecution(t, pool)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	ctx := t.Context()
	first := reserve(t, service, tenant, session, "pending")
	for _, request := range []struct {
		key    string
		inputs []sessions.Input
		want   error
	}{
		{"pending", first.Inputs, sessions.ErrTurnConflict},
		{"pending", []sessions.Input{messageInput("changed")}, sessions.ErrIdempotencyConflict},
		{"later", []sessions.Input{messageInput("later")}, sessions.ErrTurnConflict},
		{"cancel", []sessions.Input{cancelInput}, sessions.ErrTurnConflict},
	} {
		if _, err := service.SubmitInputs(ctx, text(tenant), text(session), request.key, request.inputs); !errors.Is(err, request.want) {
			t.Fatal("direct path bypassed reservation", request.key, err)
		}
	}
	environmentInputHistory(t, pool, session, 0, 0)
	promoted, err := operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), first.ID)
	if err != nil || promoted.State != sessions.EnvironmentInputAdmitted || promoted.SettledAt == nil || len(promoted.Receipts) != 2 || !promoted.Deadline.Equal(first.Deadline) {
		t.Fatal(promoted, err)
	}
	for i, receipt := range promoted.Receipts {
		if receipt.Replayed || receipt.TurnID == "" || receipt.TurnID != promoted.Receipts[0].TurnID || (i > 0 && receipt.Sequence <= promoted.Receipts[i-1].Sequence) {
			t.Fatal("promotion receipts", promoted.Receipts)
		}
	}
	environmentInputHistory(t, pool, session, 1, 2)
	for _, read := range []func() (sessions.EnvironmentInputReservation, error){
		func() (sessions.EnvironmentInputReservation, error) {
			return operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), first.ID)
		},
		func() (sessions.EnvironmentInputReservation, error) {
			return store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), first.ID)
		},
		func() (sessions.EnvironmentInputReservation, error) {
			return service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "pending", first.Inputs)
		},
	} {
		retry, err := read()
		if err != nil || retry.ID != first.ID || !retry.Deadline.Equal(first.Deadline) || retry.State != sessions.EnvironmentInputAdmitted || len(retry.Receipts) != 2 {
			t.Fatal(retry, err)
		}
		for i, receipt := range retry.Receipts {
			if !receipt.Replayed || receipt.Sequence != promoted.Receipts[i].Sequence || receipt.TurnID != promoted.Receipts[i].TurnID {
				t.Fatal("retry changed admission", receipt)
			}
		}
	}
	retry, err := service.SubmitInputs(ctx, text(tenant), text(session), "pending", first.Inputs)
	if err != nil || len(retry) != 2 || !retry[0].Replayed || retry[0].Sequence != promoted.Receipts[0].Sequence {
		t.Fatal("direct retry after promotion", retry, err)
	}
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, pool)
	if err := lease.Close(ctx); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	restarted, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	pool.Close()
	successor, _ := sessionExecution(t, restarted)
	after, err := successor.PromoteEnvironmentInput(ctx, text(tenant), text(session), first.ID)
	if err != nil || after.State != sessions.EnvironmentInputAdmitted || after.Receipts[0].Sequence != promoted.Receipts[0].Sequence {
		t.Fatal("restart repeated promotion", after, err)
	}
	environmentInputHistory(t, restarted, session, 1, 2)
}

// Reservations resolve only in their tenant's Session, take only messages,
// need an Environment and join an active Turn at once.
func TestEnvironmentInputReservationRejectsUnsupportedOrForeignState(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	ctx := t.Context()
	pending := reserve(t, service, tenant, session, "pending")
	stranger := pgID(uuid.New())
	for _, read := range []func() error{
		func() error {
			_, err := store.GetEnvironmentInputReservation(ctx, text(stranger), text(session), pending.ID)
			return err
		},
		func() error {
			_, err := operations.PromoteEnvironmentInput(ctx, text(stranger), text(session), pending.ID)
			return err
		},
		func() error {
			_, err := cancelPending(ctx, pool, store, stranger, session, pending.ID)
			return err
		},
		func() error {
			_, err := service.ReserveEnvironmentInput(ctx, text(stranger), text(session), "new", pending.Inputs)
			return err
		},
	} {
		if err := read(); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal("foreign access", err)
		}
	}
	other := pgID(uuid.New())
	exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash) VALUES ($1, $2, 'codex', 'other', 'hash')`, other, tenant)
	for _, action := range []func(context.Context, pgtype.UUID, pgtype.UUID, string) (sessions.EnvironmentInputReservation, error){
		func(ctx context.Context, tenant, session pgtype.UUID, reservation string) (sessions.EnvironmentInputReservation, error) {
			return store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), reservation)
		},
		func(ctx context.Context, tenant, session pgtype.UUID, reservation string) (sessions.EnvironmentInputReservation, error) {
			return operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), reservation)
		},
		func(ctx context.Context, tenant, session pgtype.UUID, reservation string) (sessions.EnvironmentInputReservation, error) {
			return cancelPending(ctx, pool, store, tenant, session, reservation)
		},
		func(ctx context.Context, tenant, session pgtype.UUID, reservation string) (sessions.EnvironmentInputReservation, error) {
			return service.ExpireEnvironmentInput(ctx, text(tenant), text(session), reservation)
		},
	} {
		if _, err := action(ctx, tenant, other, pending.ID); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal("reservation crossed Session ownership", err)
		}
		if _, err := action(ctx, stranger, session, pending.ID); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal("reservation crossed tenant ownership", err)
		}
	}
	retained, err := store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), pending.ID)
	if err != nil || !reflect.DeepEqual(retained, pending) {
		t.Fatal("foreign operations changed reservation", retained, err)
	}
	for _, invalid := range [][]sessions.Input{nil, {cancelInput}, {{Kind: "tool_result", Payload: []byte(`{}`)}}} {
		if _, err := service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "invalid", invalid); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatal("unsupported reservation", err)
		}
	}
	noneTenant, none := newSession(t, pool)
	if _, err := service.ReserveEnvironmentInput(ctx, text(noneTenant), text(none), "none", pending.Inputs); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("none reservation", err)
	}
	activeTenant, active, _ := newEnvironment(t, pool, "self_hosted", "pending")
	activeInput := submitMessage(t, service, activeTenant, active, "active")
	steer, err := service.ReserveEnvironmentInput(ctx, text(activeTenant), text(active), "new", pending.Inputs)
	if err != nil || steer.State != sessions.EnvironmentInputAdmitted || steer.ID != "" || !steer.Deadline.IsZero() || len(steer.Receipts) != len(pending.Inputs) || steer.Receipts[0].TurnID != activeInput.TurnID {
		t.Fatal("active input did not retain the existing Turn", steer, err)
	}
}

// A cancelled or expired reservation keeps its outcome and identity: no retry
// or settlement restarts it, and none touches a newer reservation.
func TestEnvironmentInputTerminalReservationsCannotRestart(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	for _, terminal := range []string{sessions.EnvironmentInputCancelled, sessions.EnvironmentInputExpired} {
		t.Run(terminal, func(t *testing.T) {
			tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
			ctx := t.Context()
			pending := reserve(t, service, tenant, session, "pending")
			early, err := service.ExpireEnvironmentInput(ctx, text(tenant), text(session), pending.ID)
			if err != nil || early.State != sessions.EnvironmentInputPending || early.SettledAt != nil || !early.Deadline.Equal(pending.Deadline) {
				t.Fatal("early expiry", early, err)
			}
			var settled sessions.EnvironmentInputReservation
			if terminal == sessions.EnvironmentInputExpired {
				passDeadline(t, pool, pending.ID)
				settled, err = operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), pending.ID)
			} else {
				settled, err = cancelPending(ctx, pool, store, tenant, session, pending.ID)
			}
			if err != nil || settled.State != terminal || settled.SettledAt == nil || len(settled.Receipts) != 0 {
				t.Fatal("terminal settlement", settled, err)
			}
			retry, err := service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "pending", pending.Inputs)
			if err != nil || retry.State != terminal || retry.ID != pending.ID || !retry.Deadline.Equal(settled.Deadline) || !retry.SettledAt.Equal(*settled.SettledAt) {
				t.Fatal("terminal retry changed outcome", retry, err)
			}
			if _, err := service.SubmitInputs(ctx, text(tenant), text(session), "pending", pending.Inputs); !errors.Is(err, sessions.ErrTurnConflict) {
				t.Fatal("terminal request reopened through direct path", err)
			}
			if _, err := service.SubmitInputs(ctx, text(tenant), text(session), "pending", []sessions.Input{messageInput("changed")}); !errors.Is(err, sessions.ErrIdempotencyConflict) {
				t.Fatal("terminal identity changed", err)
			}
			later := reserve(t, service, tenant, session, "later")
			// Cancellation takes whatever input is pending, so only the
			// settlements that name the reservation run against it here.
			for _, finish := range []func(context.Context, string, string, string) (sessions.EnvironmentInputReservation, error){
				operations.PromoteEnvironmentInput, service.ExpireEnvironmentInput,
			} {
				got, err := finish(ctx, text(tenant), text(session), pending.ID)
				if err != nil || got.State != terminal {
					t.Fatal("old settlement changed", got, err)
				}
			}
			got, err := store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), later.ID)
			if err != nil || got.State != sessions.EnvironmentInputPending || !got.Deadline.Equal(later.Deadline) {
				t.Fatal("old settlement touched successor", got, err)
			}
			environmentInputHistory(t, pool, session, 0, 0)
		})
	}
}

// A promotion that fails part way leaves no Turn, input, Item, claim or
// settlement, and its retry promotes the reservation whole.
func TestEnvironmentInputPromotionRollsBackHistoryAndSettlement(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	for _, phase := range []string{"input", "settlement", "claim", "claim-event"} {
		t.Run(phase, func(t *testing.T) {
			tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
			ctx := t.Context()
			pending := reserve(t, service, tenant, session, "pending")
			name := "reservation_failure_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			table, expression := "turn_inputs", "session_id <> '"+text(session)+"'::uuid OR payload#>>'{input,0,content,0,text}' <> 'second'"
			switch phase {
			case "settlement":
				table, expression = "environment_input_reservations", "id <> '"+pending.ID+"'::uuid OR state <> 'admitted'"
			case "claim":
				table, expression = "turns", "session_id <> '"+text(session)+"'::uuid OR status <> 'in_progress'"
			case "claim-event":
				table, expression = "session_events", "session_id <> '"+text(session)+"'::uuid OR payload->'event'->>'type' <> 'agent.session.turn.in_progress'"
			}
			exec(t, pool, "ALTER TABLE "+table+" ADD CONSTRAINT "+name+" CHECK ("+expression+") NOT VALID")
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), "ALTER TABLE "+table+" DROP CONSTRAINT IF EXISTS "+name)
			})
			if _, err := operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), pending.ID); err == nil {
				t.Fatal("injected failure succeeded")
			}
			environmentInputHistory(t, pool, session, 0, 0)
			got, err := store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), pending.ID)
			if err != nil || got.State != sessions.EnvironmentInputPending || got.SettledAt != nil || !got.Deadline.Equal(pending.Deadline) {
				t.Fatal("partial settlement survived", got, err)
			}
			exec(t, pool, "ALTER TABLE "+table+" DROP CONSTRAINT "+name)
			got, err = operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), pending.ID)
			if err != nil || got.State != sessions.EnvironmentInputAdmitted {
				t.Fatal(got, err)
			}
			environmentInputHistory(t, pool, session, 1, 2)
		})
	}
}

// Promotion and failure read the deadline after they take the Session lock,
// so waiting for the lock never extends the reservation's lifetime.
func TestEnvironmentInputDeadlineIsCheckedAfterSessionLock(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	for _, action := range []string{"promote", "fail"} {
		t.Run(action, func(t *testing.T) {
			tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
			pending := reserve(t, service, tenant, session, "pending")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			blocker, release := lockSession(ctx, t, pool, session)
			type outcome struct {
				value sessions.EnvironmentInputReservation
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				var got sessions.EnvironmentInputReservation
				var err error
				if action == "promote" {
					got, err = operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), pending.ID)
				} else if err = operations.FailEnvironmentInput(ctx, text(tenant), text(session), pending.ID, "runtime_preparation_failed"); err == nil {
					got, err = store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), pending.ID)
				}
				done <- outcome{got, err}
			}()
			awaitBlocked(ctx, t, pool, blocker)
			// Transaction-start time is now older than the controlled deadline.
			release("UPDATE environment_input_reservations SET deadline=clock_timestamp() WHERE id=$1", pending.ID)
			result := <-done
			if result.err != nil || result.value.State != sessions.EnvironmentInputExpired || result.value.SettledAt == nil {
				t.Fatal("lock wait extended input lifetime", result)
			}
			environmentInputHistory(t, pool, session, 0, 0)
			if state := reservationState(t, store, tenant, session, pending.ID); state != sessions.EnvironmentInputExpired {
				t.Fatal("expiry was rolled back", state)
			}
		})
	}
}

// A racing promotion and cancellation agree on one outcome.
func TestEnvironmentInputCancelAndPromotionShareOneOutcome(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	ctx := t.Context()
	pending := reserve(t, service, tenant, session, "pending")
	start := make(chan struct{})
	results := make(chan sessions.EnvironmentInputReservation, 2)
	errs := make(chan error, 2)
	for _, finish := range []func() (sessions.EnvironmentInputReservation, error){
		func() (sessions.EnvironmentInputReservation, error) {
			return operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), pending.ID)
		},
		func() (sessions.EnvironmentInputReservation, error) {
			return cancelPending(ctx, pool, store, tenant, session, pending.ID)
		},
	} {
		go func() {
			<-start
			got, err := finish()
			results <- got
			errs <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if first.State != second.State || (first.State != sessions.EnvironmentInputAdmitted && first.State != sessions.EnvironmentInputCancelled) {
		t.Fatal("competing settlements diverged", first.State, second.State)
	}
	turns, inputs := 0, 0
	if first.State == sessions.EnvironmentInputAdmitted {
		turns, inputs = 1, 2
	}
	environmentInputHistory(t, pool, session, turns, inputs)
}

// Each expiry pass settles at most 32 due reservations and creates no
// history.
func TestEnvironmentExpiryBoundsBatch(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	ctx := t.Context()
	type due struct {
		tenant, session pgtype.UUID
		reservation     string
	}
	var reservations []due
	for range 33 {
		tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
		pending := reserve(t, service, tenant, session, "pending")
		passDeadline(t, pool, pending.ID)
		reservations = append(reservations, due{tenant, session, pending.ID})
	}
	for _, want := range []int64{32, 1, 0} {
		n, err := operations.ExpireEnvironmentInputs(ctx)
		if err != nil || n != want {
			t.Fatal("unbounded or incomplete batch", n, want, err)
		}
	}
	for _, r := range reservations {
		if state := reservationState(t, store, r.tenant, r.session, r.reservation); state != sessions.EnvironmentInputExpired {
			t.Fatal(state)
		}
		environmentInputHistory(t, pool, r.session, 0, 0)
	}
}

// An execution owner that lost its lease expires nothing; its successor
// does.
func TestEnvironmentExpiryFencesLostExecutionOwner(t *testing.T) {
	pool, store, service, old := leasedInputs(t)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	pending := reserve(t, service, tenant, session, "pending")
	passDeadline(t, pool, pending.ID)
	terminateLeaseOwner(t, pool)
	successor, _ := sessionExecution(t, pool)
	if n, err := old.ExpireEnvironmentInputs(t.Context()); err == nil || n != 0 {
		t.Fatal("lost owner expired input", n, err)
	}
	if state := reservationState(t, store, tenant, session, pending.ID); state != sessions.EnvironmentInputPending {
		t.Fatal("lost owner expired input", state)
	}
	if n, err := successor.ExpireEnvironmentInputs(t.Context()); err != nil || n != 1 {
		t.Fatal("successor could not expire input", n, err)
	}
	if state := reservationState(t, store, tenant, session, pending.ID); state != sessions.EnvironmentInputExpired {
		t.Fatal(state)
	}
	environmentInputHistory(t, pool, session, 0, 0)
}

// Promotion and expiry of a reservation past its deadline, in either order or
// racing, end with one expired outcome and no history, and leave a newer
// reservation pending.
func TestEnvironmentInputPromotionSerializesWithExpiry(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	promote := func(ctx context.Context, tenant, session pgtype.UUID, reservation string) error {
		got, err := operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), reservation)
		if err == nil && got.State != sessions.EnvironmentInputExpired {
			return errors.New("promotion outran expiry: " + got.State)
		}
		return err
	}
	sweep := func(ctx context.Context, _, _ pgtype.UUID, _ string) error {
		_, err := operations.ExpireEnvironmentInputs(ctx)
		return err
	}
	expire := func(ctx context.Context, tenant, session pgtype.UUID, reservation string) error {
		got, err := service.ExpireEnvironmentInput(ctx, text(tenant), text(session), reservation)
		if err == nil && got.State != sessions.EnvironmentInputExpired {
			return errors.New("expiry missed its deadline: " + got.State)
		}
		return err
	}
	type settle func(context.Context, pgtype.UUID, pgtype.UUID, string) error
	for name, actions := range map[string][]settle{
		"sweep-then-promote": {sweep, promote},
		"promote-then-sweep": {promote, sweep},
		"racing-sweep":       {sweep, promote},
		"racing-expire":      {expire, promote},
	} {
		t.Run(name, func(t *testing.T) {
			tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
			ctx := t.Context()
			pending := reserve(t, service, tenant, session, "pending")
			passDeadline(t, pool, pending.ID)
			if strings.HasPrefix(name, "racing") {
				start := make(chan struct{})
				errs := make(chan error, len(actions))
				for _, action := range actions {
					go func() { <-start; errs <- action(ctx, tenant, session, pending.ID) }()
				}
				close(start)
				for range actions {
					if err := <-errs; err != nil {
						t.Fatal(err)
					}
				}
			} else {
				for _, action := range actions {
					if err := action(ctx, tenant, session, pending.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if state := reservationState(t, store, tenant, session, pending.ID); state != sessions.EnvironmentInputExpired {
				t.Fatal("invalid competing settlement", state)
			}
			environmentInputHistory(t, pool, session, 0, 0)
			later := reserve(t, service, tenant, session, "later")
			for _, action := range []settle{promote, expire, sweep} {
				if err := action(ctx, tenant, session, pending.ID); err != nil {
					t.Fatal("old reservation changed", err)
				}
			}
			got, err := store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), later.ID)
			if err != nil || got.State != sessions.EnvironmentInputPending || !got.Deadline.Equal(later.Deadline) {
				t.Fatal("old settlement affected successor", got, err)
			}
		})
	}
}

// Concurrent promotions claim one Turn once: one fresh admission, one claim
// event after the admission's, and a retry after the Turn ends publishes
// nothing.
func TestEnvironmentInputConcurrentPromotionClaimsOnce(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	pending := reserve(t, service, tenant, session, "pending")
	before, _ := journal(t, pool, session)
	const count = 8
	results := make(chan sessions.EnvironmentInputReservation, count)
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			got, err := operations.PromoteEnvironmentInput(t.Context(), text(tenant), text(session), pending.ID)
			if err != nil {
				t.Error(err)
				return
			}
			results <- got
		})
	}
	group.Wait()
	close(results)
	var turnID string
	fresh, received := 0, 0
	for got := range results {
		received++
		if got.State != sessions.EnvironmentInputAdmitted || len(got.Receipts) != 2 {
			t.Fatal("promotion lost the original batch", got)
		}
		if turnID == "" {
			turnID = got.Receipts[0].TurnID
		}
		if !got.Receipts[0].Replayed {
			fresh++
		}
		for _, receipt := range got.Receipts {
			if receipt.TurnID != turnID || receipt.Replayed != got.Receipts[0].Replayed {
				t.Fatal("promotion changed execution ownership", got.Receipts)
			}
		}
	}
	if received != count || fresh != 1 {
		t.Fatal("promotion authorized multiple starts", received, fresh)
	}
	turn, err := store.GetTurn(t.Context(), text(tenant), text(session), turnID)
	if err != nil || turn.Status != sessions.TurnInProgress || turn.StartedAt.IsZero() {
		t.Fatal("promotion did not persist its execution claim", turn, err)
	}
	environmentInputHistory(t, pool, session, 1, 2)
	_, changes := journal(t, pool, session)
	changes = changes[len(before):]
	if len(changes) < 2 {
		t.Fatal("missing promotion events", kinds(changes))
	}
	created, claimed := changes[0], changes[len(changes)-1]
	if created.Event.Type != "agent.session.turn.created" || created.Turn == nil || created.Turn.Status != sessions.TurnQueued || claimed.Event.Type != "agent.session.turn.in_progress" || claimed.Turn == nil || claimed.Turn.ID != turnID || claimed.Turn.Status != sessions.TurnInProgress {
		t.Fatal("claim reordered or replaced admission snapshots", kinds(changes))
	}
	claims := 0
	for _, change := range changes {
		if change.Event.Type == "agent.session.turn.in_progress" {
			claims++
		}
	}
	if claims != 1 {
		t.Fatal("retry published another claim", claims)
	}
	move(t, pool, tenant, session, turnID, sessions.TurnInProgress, sessions.TurnCompleted)
	later := reserve(t, service, tenant, session, "later")
	cursor, _ := journal(t, pool, session)
	retry, err := operations.PromoteEnvironmentInput(t.Context(), text(tenant), text(session), pending.ID)
	if err != nil || len(retry.Receipts) != 2 || !retry.Receipts[0].Replayed || retry.Receipts[0].TurnID != turnID {
		t.Fatal("terminal retry reclaimed execution", retry, err)
	}
	if after, _ := journal(t, pool, session); !reflect.DeepEqual(after, cursor) {
		t.Fatal("terminal retry published events", after, cursor)
	}
	retained, err := store.GetEnvironmentInputReservation(t.Context(), text(tenant), text(session), later.ID)
	if err != nil || retained.State != sessions.EnvironmentInputPending || !retained.Deadline.Equal(later.Deadline) {
		t.Fatal("old promotion affected new preparation", retained, err)
	}
}

// Promotion and preparation failure from a closed lease or a lost execution
// owner write nothing; the successor settles both.
func TestEnvironmentInputSettlementUsesCurrentExecutionOwner(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	store, service := stagingService(t, pool)
	promoteTenant, promoteSession, _ := newEnvironment(t, pool, "self_hosted", "pending")
	failTenant, failSession, _ := newEnvironment(t, pool, "self_hosted", "pending")
	promoted := reserve(t, service, promoteTenant, promoteSession, "pending")
	failed := reserve(t, service, failTenant, failSession, "pending")
	settle := func(operations *sessions.ExecutionOperations) (error, error) {
		_, promoteErr := operations.PromoteEnvironmentInput(t.Context(), text(promoteTenant), text(promoteSession), promoted.ID)
		return promoteErr, operations.FailEnvironmentInput(t.Context(), text(failTenant), text(failSession), failed.ID, "runtime_preparation_failed")
	}
	unsettled := func() {
		t.Helper()
		environmentInputHistory(t, pool, promoteSession, 0, 0)
		environmentInputHistory(t, pool, failSession, 0, 0)
		if reservationState(t, store, promoteTenant, promoteSession, promoted.ID) != sessions.EnvironmentInputPending || reservationState(t, store, failTenant, failSession, failed.ID) != sessions.EnvironmentInputPending {
			t.Fatal("fenced owner settled input")
		}
	}
	closed, lease := sessionExecution(t, pool)
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, pool)
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	if promoteErr, failErr := settle(closed); !errors.Is(promoteErr, pgunit.ErrLeaseClosed) || !errors.Is(failErr, pgunit.ErrLeaseClosed) {
		t.Fatal("closed lease settled input", promoteErr, failErr)
	}
	unsettled()
	stale, _ := sessionExecution(t, pool)
	terminateLeaseOwner(t, pool)
	successor, _ := sessionExecution(t, pool)
	if promoteErr, failErr := settle(stale); promoteErr == nil || failErr == nil {
		t.Fatal("stale owner settled input", promoteErr, failErr)
	}
	unsettled()
	if promoteErr, failErr := settle(successor); promoteErr != nil || failErr != nil {
		t.Fatal("successor could not settle", promoteErr, failErr)
	}
	environmentInputHistory(t, pool, promoteSession, 1, 2)
	environmentInputHistory(t, pool, failSession, 0, 0)
	if reservationState(t, store, promoteTenant, promoteSession, promoted.ID) != sessions.EnvironmentInputAdmitted || reservationState(t, store, failTenant, failSession, failed.ID) != sessions.EnvironmentInputFailed {
		t.Fatal("successor settlement")
	}
}

// Input reserved while the active Turn completes serializes with the
// completion: input first joins the Turn and blocks completion until applied;
// completion first leaves the input pending for its own Turn.
func TestEnvironmentActiveInputSerializesWithCompletion(t *testing.T) {
	pool, _, service, operations := leasedInputs(t)
	for _, completionFirst := range []bool{false, true} {
		name := "input-first"
		if completionFirst {
			name = "completion-first"
		}
		t.Run(name, func(t *testing.T) {
			tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
			original := submitMessage(t, service, tenant, session, "original")
			move(t, pool, tenant, session, original.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			blocker, release := lockSession(ctx, t, pool, session)
			type admission struct {
				value sessions.EnvironmentInputReservation
				err   error
			}
			admitted := make(chan admission, 1)
			completed := make(chan error, 1)
			input := func() {
				value, err := service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "racing-input", reservedBatch)
				admitted <- admission{value, err}
			}
			complete := func() {
				_, err := operations.CompleteExecution(ctx, text(tenant), text(session), original.TurnID, sessions.TurnCompleted, nil, "", original.Sequence)
				completed <- err
			}
			first, second := input, complete
			if completionFirst {
				first, second = complete, input
			}
			go first()
			firstPID := awaitBlocked(ctx, t, pool, blocker)
			go second()
			awaitBlocked(ctx, t, pool, firstPID)
			release("")
			got, completionErr := <-admitted, <-completed
			if got.err != nil {
				t.Fatal(got.err)
			}
			if completionFirst {
				if completionErr != nil || got.value.State != sessions.EnvironmentInputPending || got.value.ID == "" || len(got.value.Receipts) != 0 || got.value.Deadline.Sub(got.value.CreatedAt) != 5*time.Minute {
					t.Fatal("completion winner did not leave new input waiting for preparation", completionErr, got.value)
				}
				environmentInputHistory(t, pool, session, 1, 1)
				prepared, err := operations.PromoteEnvironmentInput(ctx, text(tenant), text(session), got.value.ID)
				if err != nil || len(prepared.Receipts) != 2 || prepared.Receipts[0].TurnID == original.TurnID {
					t.Fatal("prepared successor reused terminal work", err)
				}
				retry, err := service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "racing-input", reservedBatch)
				if err != nil || retry.ID != got.value.ID || !retry.Deadline.Equal(got.value.Deadline) || len(retry.Receipts) != 2 || !retry.Receipts[0].Replayed || retry.Receipts[0].TurnID != prepared.Receipts[0].TurnID {
					t.Fatal("active retry replaced its original reservation", retry, err)
				}
				if _, err := operations.CompleteExecution(ctx, text(tenant), text(session), prepared.Receipts[0].TurnID, sessions.TurnCompleted, nil, "", prepared.Receipts[1].Sequence); err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(completionErr, sessions.ErrUnappliedInputs) || got.value.State != sessions.EnvironmentInputAdmitted || got.value.ID != "" || !got.value.Deadline.IsZero() || len(got.value.Receipts) != 2 || got.value.Receipts[0].TurnID != original.TurnID || got.value.Receipts[0].Replayed {
					t.Fatal("admitted input escaped the original Turn or application fence", completionErr, got.value)
				}
				environmentInputHistory(t, pool, session, 1, 3)
				if _, err := operations.CompleteExecution(ctx, text(tenant), text(session), original.TurnID, sessions.TurnCompleted, nil, "", got.value.Receipts[1].Sequence); err != nil {
					t.Fatal("completion after controlled application failed", err)
				}
			}
		})
	}
}

// A late preparation failure leaves a cancelled reservation and a newer one
// as they are, and takes only classified codes.
func TestPreparationFailurePreservesCancelledAndNewerInput(t *testing.T) {
	pool, store, service, operations := leasedInputs(t)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	first := reserve(t, service, tenant, session, "cancelled")
	if _, err := cancelPending(t.Context(), pool, store, tenant, session, first.ID); err != nil {
		t.Fatal(err)
	}
	next := reserve(t, service, tenant, session, "new")
	if err := operations.FailEnvironmentInput(t.Context(), text(tenant), text(session), first.ID, "runtime_preparation_failed"); err != nil {
		t.Fatal(err)
	}
	for id, state := range map[string]string{first.ID: sessions.EnvironmentInputCancelled, next.ID: sessions.EnvironmentInputPending} {
		if current := reservationState(t, store, tenant, session, id); current != state {
			t.Fatal("late failure changed another outcome", id, current)
		}
	}
	if err := operations.FailEnvironmentInput(t.Context(), text(tenant), text(session), next.ID, "secret-canary"); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("unclassified diagnostic accepted", err)
	}
}
