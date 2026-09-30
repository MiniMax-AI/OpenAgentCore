package pgunit

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
)

// ExecutionTimeout bounds every leased operation, including its wait for the
// connection gate and for row locks. Code that groups several statements under
// one execution deadline uses it too.
const ExecutionTimeout = 5 * time.Second

var (
	ErrLeaseHeld   = errors.New("another execution service owns this database")
	ErrLeaseClosed = errors.New("execution lease is closed")
)

// Lease owns the connection used for execution writes, not just election: the
// PostgreSQL advisory lock belongs to that session, so every execution write runs
// on it. Its gate serializes pgx operations on the connection; no daemon or model
// work holds the gate. Losing or closing the lease never falls back to a pooled
// connection.
type Lease struct {
	conn        *pgxpool.Conn
	gate        chan struct{}
	cleanupDone <-chan struct{}
}

// AcquireLease takes the database's execution lease on a dedicated connection
// from pool. It enforces single-service ownership per database and fails with
// ErrLeaseHeld when another service owns it.
func AcquireLease(ctx context.Context, pool *pgxpool.Pool) (*Lease, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	acquired, err := sqlc.New(conn).TryExecutionLease(ctx)
	if err != nil || !acquired {
		_ = conn.Hijack().Close(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, ErrLeaseHeld
	}
	return &Lease{conn: conn, gate: make(chan struct{}, 1)}, nil
}

// Transaction runs apply in a read committed transaction on the leased
// connection and commits only when apply returns nil. apply receives the
// context carrying the execution deadline.
func (l *Lease) Transaction(ctx context.Context, apply func(context.Context, pgx.Tx) error) error {
	return l.withConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		return run(ctx, conn, readWrite, apply)
	})
}

// CheckOwnership pings the leased connection, confirming that this service
// still owns the database before external work.
func (l *Lease) CheckOwnership(ctx context.Context) error {
	return l.withConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error { return conn.Ping(ctx) })
}

// CancelOperations cancels coordinator-owned contexts between leased
// operations. Cancelling an in-flight pgx operation can close the connection
// that owns the advisory lock, so cancel runs only while the gate is held and
// after the connection answers a ping. cancel must only invoke synchronous
// context cancel functions; it must not perform database, provider or wait work.
// Caller cancellation and operation deadlines keep their own semantics.
func (l *Lease) CancelOperations(ctx context.Context, cancel context.CancelFunc) error {
	if cancel == nil {
		return errors.New("execution lease cancellation requires a cancel function")
	}
	return l.withConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		if err := conn.Ping(ctx); err != nil {
			return err
		}
		cancel()
		return nil
	})
}

// Close releases the lease by closing its connection and waits, within ctx,
// for the driver's asynchronous cleanup. A later Close resumes that wait.
func (l *Lease) Close(ctx context.Context) error {
	if err := l.lock(ctx); err != nil {
		return err
	}
	defer l.unlock()
	if l.conn != nil {
		conn := l.conn.Hijack()
		l.conn = nil
		l.cleanupDone = conn.PgConn().CleanupDone()
		if err := conn.Close(ctx); err != nil {
			return err
		}
	}
	if l.cleanupDone == nil {
		return nil
	}
	// A cancelled pgx connection can be unusable before its asynchronous cleanup ends.
	// Retain the channel so a later Close can continue waiting after this deadline.
	select {
	case <-l.cleanupDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// withConn applies the execution deadline to the gate wait and the operation.
func (l *Lease) withConn(ctx context.Context, apply func(context.Context, *pgxpool.Conn) error) error {
	ctx, cancel := context.WithTimeout(ctx, ExecutionTimeout)
	defer cancel()
	if err := l.lock(ctx); err != nil {
		return err
	}
	defer l.unlock()
	if l.conn == nil {
		return ErrLeaseClosed
	}
	return apply(ctx, l.conn)
}

func (l *Lease) lock(ctx context.Context) error {
	select {
	case l.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			l.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Lease) unlock() { <-l.gate }
