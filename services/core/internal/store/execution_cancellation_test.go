package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecutionCancellationFenceHonorsBounds(t *testing.T) {
	s, _ := testStore(t)
	lease := executionLease(t, s)
	for _, test := range []struct {
		name           string
		limit, maximum time.Duration
	}{
		{"caller", 25 * time.Millisecond, time.Second},
		{"operation", 8 * time.Second, 6 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := lease.lock(t.Context()); err != nil {
				t.Fatal(err)
			}
			held := true
			defer func() {
				if held {
					lease.unlock()
				}
			}()
			operation, stopOperation := context.WithCancel(t.Context())
			defer stopOperation()
			ctx, cancel := context.WithTimeout(t.Context(), test.limit)
			defer cancel()
			started := time.Now()
			err := lease.Store().CancelExecutionOperations(ctx, stopOperation)
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > test.maximum {
				t.Fatal("cancellation fence did not preserve its deadline", err, time.Since(started))
			}
			if operation.Err() != nil {
				t.Fatal("timed-out fence canceled operations outside the lease gate")
			}
			lease.unlock()
			held = false
			if err := lease.Store().CheckExecutionOwnership(t.Context()); err != nil {
				t.Fatal("gate timeout damaged the healthy owner", err)
			}
			if err := lease.Store().CancelExecutionOperations(t.Context(), stopOperation); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(operation.Err(), context.Canceled) {
				t.Fatal("successful fence did not cancel synchronously")
			}
			if err := lease.Store().CheckExecutionOwnership(t.Context()); err != nil {
				t.Fatal("cancellation damaged the healthy owner", err)
			}
		})
	}
}

func TestExecutionCancellationFenceRejectsLostLease(t *testing.T) {
	s, pool := testStore(t)
	lease := executionLease(t, s)
	var killed bool
	if err := pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", lease.conn.Conn().PgConn().PID()).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	operation, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := lease.Store().CancelExecutionOperations(t.Context(), cancel); err == nil {
		t.Fatal("lost lease accepted cancellation fence")
	}
	if operation.Err() != nil {
		t.Fatal("lost lease ran the cancellation callback")
	}
	if err := lease.Store().CheckExecutionOwnership(t.Context()); err == nil {
		t.Fatal("lost owner became writable")
	}
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Store().CancelExecutionOperations(t.Context(), cancel); err == nil {
		t.Fatal("closed lease accepted cancellation fence")
	}
	if err := s.CancelExecutionOperations(t.Context(), cancel); err == nil {
		t.Fatal("pooled Store accepted execution cancellation")
	}
}
