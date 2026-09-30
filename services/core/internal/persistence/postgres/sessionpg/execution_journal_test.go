package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// leasedJournal holds the execution lease on a database of its own and returns
// the Session execution operations bound to it.
func leasedJournal(t *testing.T) (*pgxpool.Pool, *pgunit.Lease, *sessions.ExecutionOperations) {
	t.Helper()
	pool := pgtest.OpenIsolated(t, nil)
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	operations, err := sessions.NewExecutionOperations(NewExecution(lease))
	if err != nil {
		t.Fatal(err)
	}
	return pool, lease, operations
}

func text(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

// recorded reads the Turn's journal kinds and counters.
func recorded(t *testing.T, pool *pgxpool.Pool, turn pgtype.UUID) ([]string, int32, int64) {
	t.Helper()
	var kinds []string
	var count int32
	var size int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE((SELECT array_agg(kind ORDER BY ordinal) FROM turn_events WHERE turn_id = t.id), '{}'), t.event_count, t.event_bytes FROM turns t WHERE t.id = $1`, turn).Scan(&kinds, &count, &size); err != nil {
		t.Fatal(err)
	}
	return kinds, count, size
}

func observation(kind, payload string) sessions.ExecutionEvent {
	return sessions.ExecutionEvent{Kind: kind, Payload: json.RawMessage(payload)}
}

func TestTurnJournalRecordsReplaysAndRollsBack(t *testing.T) {
	pool, lease, operations := leasedJournal(t)
	tenant, session, turn := newTurn(t, pool, sessions.TurnInProgress)
	record := func(tenant pgtype.UUID, first int32, events ...sessions.ExecutionEvent) error {
		return operations.AppendTurnEvents(t.Context(), text(tenant), text(session), text(turn), first, events)
	}
	batch := []sessions.ExecutionEvent{observation("output_message", `{"id":"m1","status":"completed","text":"hi"}`), observation("done", `{}`)}
	if err := record(tenant, 1, batch...); err != nil {
		t.Fatal(err)
	}
	entries, count, size := recorded(t, pool, turn)
	payloadBytes := int64(len(`{"id":"m1","status":"completed","text":"hi"}`) + len(`{}`))
	if strings.Join(entries, ",") != "output_message,done" || count != 2 || size != payloadBytes {
		t.Fatalf("journal %q, %d entries, %d bytes", entries, count, size)
	}
	_, changes := journal(t, pool, session)
	if len(changes) == 0 || changes[0].Event.Type != "agent.session.turn.item.added" {
		t.Fatalf("projected changes %q", kinds(changes))
	}

	if err := record(tenant, 1, batch...); err != nil {
		t.Fatal("replay", err)
	}
	if err := record(tenant, 1, observation("done", `{"other":true}`)); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("different replay", err)
	}
	if err := record(tenant, 4, observation("done", `{}`)); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatal("gap", err)
	}
	if err := record(pgID(uuid.New()), 3, observation("done", `{}`)); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("other tenant", err)
	}
	invalid := observation("tool_call", `{"id":"call","stage":"after","observation":{"status":"completed","kind":"invalid"}}`)
	if err := record(tenant, 3, observation("done", `{}`), invalid); err == nil {
		t.Fatal("unprojectable batch recorded")
	}
	if entries, count, _ := recorded(t, pool, turn); len(entries) != 2 || count != 2 {
		t.Fatalf("failed batch kept %q, %d entries", entries, count)
	}
	_, replayed := journal(t, pool, session)
	if len(replayed) != len(changes) {
		t.Fatalf("replays journaled %d changes, want %d", len(replayed), len(changes))
	}

	exec(t, pool, `UPDATE turns SET event_count = 65536 WHERE id = $1`, turn)
	if err := record(tenant, 65537, observation("done", `{}`)); !errors.Is(err, sessions.ErrEventLimit) {
		t.Fatal("journal limit", err)
	}
	exec(t, pool, `UPDATE turns SET event_count = 2 WHERE id = $1`, turn)
	exec(t, pool, `UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1`, session)
	if err := record(tenant, 3, observation("done", `{}`)); err != nil {
		t.Fatal("deleted Session", err)
	}
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := record(tenant, 4, observation("done", `{}`)); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("closed lease", err)
	}
}

func TestSubagentProjectionStoresTheSessionRows(t *testing.T) {
	pool, _, operations := leasedJournal(t)
	tenant, session, turn, device := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration)
		VALUES ($1, $2, 'codex', 'key', 'hash', '{"agent":{"id":"agent_root"}}')`, session, tenant)
	exec(t, pool, `INSERT INTO turns(id, session_id, status) VALUES ($1, $2, 'in_progress')`, turn, session)
	exec(t, pool, `INSERT INTO devices(id, tenant_id, name, credential_hash) VALUES ($1, $2, 'device', repeat('a', 64))`, device, tenant)
	exec(t, pool, `INSERT INTO session_devices(session_id, device_id, native_session_id) VALUES ($1, $2, 'root')`, session, device)
	encode := func(kind string, value any) sessions.ExecutionEvent {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return sessions.ExecutionEvent{Kind: kind, Payload: raw}
	}
	record := func(first int32, events ...sessions.ExecutionEvent) error {
		return operations.AppendTurnEvents(t.Context(), tenant.String(), session.String(), turn.String(), first, events)
	}
	name, answer := "reviewer", "checked"
	created, completed := int64(1700000000000), int64(1700000001000)
	identity := proto.SubagentIdentityPayload{NativeID: "child", ParentNativeID: "root", NativeCreatedAt: 1700000000, ParentTurnID: "turn", SourceItemID: "item", Name: &name}
	running := proto.SubagentTurnPayload{NativeID: "child", TurnID: "native-turn", Status: sessions.TurnInProgress, CreatedAtMS: created}
	message, _ := json.Marshal(proto.OutputMessagePayload{ID: "answer", Status: "completed", Text: &answer})
	if err := record(1,
		encode(proto.TypeSubagentIdentity, identity),
		encode(proto.TypeSubagentTurn, running),
		encode(proto.TypeSubagentItem, proto.SubagentItemPayload{NativeID: "child", TurnID: "native-turn", ItemID: "answer", Position: 1, Kind: proto.TypeOutputMessage, Payload: message}),
	); err != nil {
		t.Fatal(err)
	}

	moved := running
	moved.CreatedAtMS = created + 1
	if err := record(4, encode(proto.TypeSubagentTurn, moved)); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("child Turn identity changed", err)
	}
	reparented := identity
	reparented.ParentNativeID = "unknown"
	if err := record(4, encode(proto.TypeSubagentIdentity, reparented)); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("binding changed", err)
	}
	ended := running
	ended.Status, ended.CompletedAtMS = sessions.TurnCompleted, &completed
	if err := record(4, encode(proto.TypeSubagentTurn, ended)); err != nil {
		t.Fatal(err)
	}

	var subagent, childTurn string
	inTx(t, pool, func(q *sqlc.Queries) error {
		bound := BindSession(q, pgID(tenant), pgID(session))
		child, found, err := bound.LoadNativeSubagent(t.Context(), "child")
		if err != nil || !found || !child.Visible || child.NativeCreatedAt != 1700000000 {
			t.Fatalf("native Subagent %+v, %v, %v", child, found, err)
		}
		public, err := loadPublicSubagent(t.Context(), q, pgID(session), child.ID)
		if err != nil || public.ParentAgentID != "agent_root" || public.Name == nil || *public.Name != name {
			t.Fatalf("public Subagent %+v, %v", public, err)
		}
		subagent = child.ID
		return nil
	})
	var status string
	var completedAt pgtype.Timestamptz
	if err := pool.QueryRow(t.Context(), `SELECT id::text, status, completed_at FROM subagent_turns WHERE session_id = $1 AND subagent_id = $2`, session, subagent).Scan(&childTurn, &status, &completedAt); err != nil {
		t.Fatal(err)
	}
	if status != sessions.TurnCompleted || completedAt.Time.UnixMilli() != completed {
		t.Fatalf("child Turn %s at %v", status, completedAt.Time)
	}
	var position int32
	var index *int32
	if err := pool.QueryRow(t.Context(), `SELECT position, output_index FROM subagent_items WHERE turn_id = $1`, childTurn).Scan(&position, &index); err != nil {
		t.Fatal(err)
	}
	if position != 2 || index == nil || *index != 0 {
		t.Fatalf("child Item at %d, output index %v", position, index)
	}
	_, changes := journal(t, pool, pgID(session))
	if strings.Join(kinds(changes), ",") != "agent.session.subagent.created" {
		t.Fatalf("Session changes %q", kinds(changes))
	}
	if journaled, count, _ := recorded(t, pool, pgID(turn)); len(journaled) != 4 || count != 4 {
		t.Fatalf("journal %q, %d entries", journaled, count)
	}
}
