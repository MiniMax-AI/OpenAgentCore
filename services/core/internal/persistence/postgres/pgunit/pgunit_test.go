package pgunit

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
)

// Pool-bound queries read committed rows outside any transaction.
func TestPoolQueriesReadOutsideTransactions(t *testing.T) {
	pool := NewPool(pgtest.Open(t))
	tenant := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	cursor := sqlc.GetWriteAuditCursorParams{TenantID: tenant, ID: id}
	if _, err := pool.Queries().GetWriteAuditCursor(t.Context(), cursor); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing row: %v", err)
	}
	if err := pool.Transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := sqlc.New(tx).InsertWriteAuditOperation(ctx, sqlc.InsertWriteAuditOperationParams{ID: id, TenantID: tenant, KeyID: "key", KeyName: "key", KeyPrefix: "key", KeyKind: "issued", Action: "create", ResourceType: "agent", ResourceID: "agent", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if created, err := pool.Queries().GetWriteAuditCursor(t.Context(), cursor); err != nil || !created.Valid {
		t.Fatalf("committed row: %v %v", created, err)
	}
}
