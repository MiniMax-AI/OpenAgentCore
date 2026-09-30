// Package pgunit runs Core's PostgreSQL transactions: pooled transactions for
// public and administrative work, and the execution owner's transactions on the
// connection that holds the database's execution lease.
package pgunit

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Transactions state their isolation instead of inheriting the server default:
// Core's lock-then-read code relies on read committed statement snapshots.
var (
	readWrite = pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	snapshot  = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
)

// Pool runs transactions on pooled connections. It grants no execution authority.
type Pool struct{ pool *pgxpool.Pool }

func NewPool(pool *pgxpool.Pool) *Pool { return &Pool{pool: pool} }

// Transaction runs apply in a read committed transaction and commits only when
// apply returns nil.
func (p *Pool) Transaction(ctx context.Context, apply func(context.Context, pgx.Tx) error) error {
	return run(ctx, p.pool, readWrite, apply)
}

// Snapshot runs apply in a read-only repeatable read transaction, so every
// statement reads the same snapshot.
func (p *Pool) Snapshot(ctx context.Context, apply func(context.Context, pgx.Tx) error) error {
	return run(ctx, p.pool, snapshot, apply)
}

// run passes apply the context that bounds the transaction; statements must use
// it so that they share the transaction's deadline.
func run(ctx context.Context, db interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}, options pgx.TxOptions, apply func(context.Context, pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, db, options, func(tx pgx.Tx) error { return apply(ctx, tx) })
}
