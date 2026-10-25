package sessionpg

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestListSessionsPagesFromDeletedCursor(t *testing.T) {
	pool := pgtest.Open(t)
	store := New(pgunit.NewPool(pool), pgtest.CredentialKey(t))
	tenant := uuid.New()
	var ids []string
	for i := range 3 {
		id := uuid.New()
		exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration, created_at)
			VALUES ($1, $2, 'codex', $3, 'hash', '{}', now() + $4 * interval '1 second')`, id, tenant, id.String(), i)
		ids = append(ids, id.String())
	}
	exec(t, pool, `UPDATE sessions SET deleted_at = now() WHERE id = $1`, ids[1])

	page, err := store.ListSessions(t.Context(), tenant.String(), ids[1], 10, false, nil)
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != ids[0] {
		t.Fatalf("deleted cursor: %+v %v", page, err)
	}
	if _, err := store.ListSessions(t.Context(), uuid.NewString(), ids[1], 10, false, nil); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("foreign tenant cursor: %v", err)
	}
}
