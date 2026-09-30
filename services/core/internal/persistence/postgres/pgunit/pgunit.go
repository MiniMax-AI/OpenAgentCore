// Package pgunit runs Core's PostgreSQL transactions: pooled transactions for
// public and administrative work, and the execution owner's transactions on the
// connection that holds the database's execution lease.
package pgunit

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
)

// Transactions state their isolation instead of inheriting the server default:
// Core's lock-then-read code relies on read committed statement snapshots.
var (
	readWrite = pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	snapshot  = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
)

// Transactor runs read committed transactions: a Pool on pooled connections,
// or the Lease on the execution connection.
type Transactor interface {
	Transaction(ctx context.Context, apply func(context.Context, pgx.Tx) error) error
}

var (
	_ Transactor = (*Pool)(nil)
	_ Transactor = (*Lease)(nil)
)

// Pool runs transactions on pooled connections. It grants no execution authority.
type Pool struct{ pool *pgxpool.Pool }

func NewPool(pool *pgxpool.Pool) *Pool { return &Pool{pool: pool} }

// Queries returns queries bound to the pool, outside any transaction. Use it
// only for a read that is a single statement and needs no transaction, such as
// a per-request key lookup, where Snapshot would add BEGIN and COMMIT round
// trips. A read of several statements uses Snapshot, and a write uses
// Transaction.
func (p *Pool) Queries() *sqlc.Queries { return sqlc.New(p.pool) }

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
