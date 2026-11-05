package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// An attach that commits while an Environment transaction waits for the lock
// is seen after the wait: the transaction retries with the attached Session
// locked instead of running without it.
func TestEnvironmentLockSeesAttachCommittedWhileWaiting(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	store, service := creationService(t, pool)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	tenant := uuid.NewString()
	session, err := service.CreateSession(ctx, tenant, sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "key", Configuration: environmentConfiguration})
	if err != nil {
		t.Fatal(err)
	}
	environment := uuid.New()
	exec(t, pool, `INSERT INTO environments(id, tenant_id, configuration, session_owned, initialization, idempotency_key, request_hash, creator_kind, creator_id)
		VALUES ($1, $2, '{"type":"openai_hosted"}', false, 'complete', 'key', repeat('a', 64), 'user', 'creator')`, environment, tenant)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var holder int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM environments WHERE id=$1 FOR UPDATE", environment).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	attached := make(chan pgtype.UUID, 1)
	go func() {
		var last pgtype.UUID
		err := WithEnvironment(ctx, store.units, pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true}, pgtype.UUID{Bytes: environment, Valid: true},
			func(_ context.Context, _ *sqlc.Queries, _ sessions.LockedSession, session pgtype.UUID) error {
				last = session
				return nil
			})
		if err != nil {
			t.Error(err)
		}
		attached <- last
	}()
	awaitBlocked(ctx, t, pool, holder)
	if _, err := tx.Exec(ctx, "UPDATE sessions SET environment_id=$1 WHERE id=$2", environment, session.Session.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := <-attached; !got.Valid || uuid.UUID(got.Bytes).String() != session.Session.ID {
		t.Fatal("the waiting transaction missed the attached Session", got)
	}
}

// A creation key deduplicates within the creator: another creator reusing it
// creates its own Environment.
func TestEnvironmentCreationKeyIsScopedToTheCreator(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	_, service := creationService(t, pool)
	tenant := uuid.NewString()
	exec(t, pool, `UPDATE runtime_deployment SET installation_id=$1, backend_fingerprint=$2, provider_kind='e2b', mode='direct', generation=1,
		specification='{"resources":{"cpus":2,"memory_mib":2048}}'`, uuid.New(), strings.Repeat("a", 64))
	create := func(who identity.Subject) string {
		id, err := service.CreateEnvironment(t.Context(), tenant, sessions.CreateEnvironment{
			Creator: who, IdempotencyKey: "shared", CreationRequest: json.RawMessage(`{}`), Configuration: json.RawMessage(`{"type":"openai_hosted"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, other := create(creator), create(identity.Subject{Kind: "service_account", ID: "other"})
	if first == other || create(creator) != first {
		t.Fatal("the key crossed creators or did not replay", first, other)
	}
}

// Attaching a Session meets the deployment state a new Environment meets: a
// pending reset refuses it.
func TestAttachIsRefusedWhileAResetIsPending(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	_, service := creationService(t, pool)
	tenant, environment := uuid.NewString(), uuid.New()
	exec(t, pool, `UPDATE runtime_deployment SET installation_id=$1, backend_fingerprint=$2, provider_kind='e2b', mode='direct', generation=1,
		specification='{"resources":{"cpus":2,"memory_mib":2048}}', reset_clear='force', reset_requested_at=now(), reset_forced_at=now(), reset_audit='{}'`, uuid.New(), strings.Repeat("a", 64))
	exec(t, pool, `INSERT INTO environments(id, tenant_id, configuration, session_owned, initialization, idempotency_key, request_hash, creator_kind, creator_id)
		VALUES ($1, $2, '{"type":"openai_hosted"}', false, 'complete', 'key', repeat('a', 64), 'user', 'creator')`, environment, tenant)
	_, err := service.CreateSession(t.Context(), tenant, sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "attach", EnvironmentID: environment.String(),
		Configuration: json.RawMessage(`{"agent":{"model":"m"},"environment":{"type":"openai_hosted"}}`)})
	if !errors.Is(err, placement.ErrResetAdmission) {
		t.Fatal("attach bypassed the pending reset", err)
	}
}
