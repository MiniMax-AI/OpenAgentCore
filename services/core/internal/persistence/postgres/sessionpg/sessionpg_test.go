package sessionpg

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
)

// newTurn stores a fresh tenant's Session with one Turn of status and returns
// their IDs.
func newTurn(t *testing.T, pool *pgxpool.Pool, status string) (pgtype.UUID, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	tenant, session, turn := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash) VALUES ($1, $2, 'codex', 'key', 'hash')`, session, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO turns(id, session_id, status, completed_at)
		VALUES ($1, $2, $3::text, CASE WHEN $3::text IN ('completed', 'failed', 'cancelled') THEN clock_timestamp() END)`, turn, session, status); err != nil {
		t.Fatal(err)
	}
	return pgtype.UUID{Bytes: tenant, Valid: true}, pgtype.UUID{Bytes: session, Valid: true}, pgtype.UUID{Bytes: turn, Valid: true}
}

// inTx runs apply in one committed transaction on transaction-bound queries.
func inTx(t *testing.T, pool *pgxpool.Pool, apply func(*sqlc.Queries) error) {
	t.Helper()
	if err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error { return apply(sqlc.New(tx)) }); err != nil {
		t.Fatal(err)
	}
}

func journal(t *testing.T, pool *pgxpool.Pool, session pgtype.UUID) ([]int64, []sessions.SessionChange) {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT sequence, payload FROM session_events WHERE session_id = $1 ORDER BY sequence`, session)
	if err != nil {
		t.Fatal(err)
	}
	var sequences []int64
	var changes []sessions.SessionChange
	for rows.Next() {
		var sequence int64
		var change sessions.SessionChange
		if err := rows.Scan(&sequence, &change); err != nil {
			t.Fatal(err)
		}
		sequences, changes = append(sequences, sequence), append(changes, change)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return sequences, changes
}

func TestAppendChangesSequencesAndPruneKeepsTheNewest(t *testing.T) {
	pool := pgtest.Open(t)
	_, session, _ := newTurn(t, pool, sessions.TurnInProgress)
	total := sessions.RetainedChanges + 2
	inTx(t, pool, func(q *sqlc.Queries) error {
		for range total {
			if err := AppendChanges(t.Context(), q, session, sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.in_progress"}}); err != nil {
				return err
			}
		}
		return PruneChanges(t.Context(), q, session)
	})
	sequences, changes := journal(t, pool, session)
	if len(sequences) != sessions.RetainedChanges || sequences[0] != 3 || sequences[len(sequences)-1] != int64(total) {
		t.Fatalf("retained %d changes from %d to %d", len(sequences), sequences[0], sequences[len(sequences)-1])
	}
	ids := map[string]bool{}
	for _, change := range changes {
		if change.Event.SessionID != uuid.UUID(session.Bytes).String() || uuid.Validate(change.Event.EventID) != nil || ids[change.Event.EventID] {
			t.Fatalf("change identity %+v", change.Event)
		}
		ids[change.Event.EventID] = true
	}
}

func TestItemsTakePositionsAndOutputIndexes(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, turn := newTurn(t, pool, sessions.TurnInProgress)
	turnID := uuid.UUID(turn.Bytes).String()
	created := time.Now()
	text := func(value string) *string { return &value }
	input := v1.Item{ID: uuid.NewString(), TurnID: turnID, Type: "message", Role: "user", Status: "completed", Content: []v1.ItemContent{{Type: "input_text", Text: text("hi")}}}
	answer := v1.Item{ID: uuid.NewString(), TurnID: turnID, Type: "message", Role: "assistant", Status: "in_progress", Content: []v1.ItemContent{{Type: "output_text", Text: text("draft")}}}
	search := v1.Item{ID: uuid.NewString(), TurnID: turnID, Type: "web_search_call", Status: "in_progress"}
	var indexes []*int32
	inTx(t, pool, func(q *sqlc.Queries) error {
		for _, change := range []items.Change{{Item: input}, {Item: answer, Output: true}, {Item: search, Output: true}, {Previous: answer, Item: answer, Output: true}} {
			index, err := BindSession(q, tenant, session).PutItem(t.Context(), turnID, created, change)
			if err != nil {
				return err
			}
			indexes = append(indexes, index)
		}
		return nil
	})
	if indexes[0] != nil || *indexes[1] != 0 || *indexes[2] != 1 || *indexes[3] != 0 {
		t.Fatalf("output indexes %v", indexes)
	}
	rows, err := pool.Query(t.Context(), `SELECT id::text FROM session_items WHERE session_id = $1 ORDER BY position`, session)
	if err != nil {
		t.Fatal(err)
	}
	positions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || !reflect.DeepEqual(positions, []string{input.ID, answer.ID, search.ID}) {
		t.Fatalf("Session positions %q, %v", positions, err)
	}
	inTx(t, pool, func(q *sqlc.Queries) error {
		bound := BindSession(q, tenant, session)
		stored, err := bound.LoadItem(t.Context(), turnID, items.Update{Item: v1.Item{ID: answer.ID}})
		if err != nil || !reflect.DeepEqual(stored, items.Stored{Item: answer}) {
			t.Fatalf("stored %+v, %v", stored, err)
		}
		legacy, err := bound.LoadItem(t.Context(), turnID, items.Update{Item: v1.Item{ID: uuid.NewString()}, LegacyFinal: true})
		if err != nil || !legacy.NativeMessage || legacy.Item.ID != "" {
			t.Fatalf("legacy aggregate %+v, %v", legacy, err)
		}
		return nil
	})
}

func TestUnstorableTextIsTheSharedError(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, turn := newTurn(t, pool, sessions.TurnInProgress)
	nul := "a\x00b"
	item := v1.Item{ID: uuid.NewString(), TurnID: uuid.UUID(turn.Bytes).String(), Type: "message", Role: "user", Status: "completed", Content: []v1.ItemContent{{Type: "input_text", Text: &nul}}}
	for name, write := range map[string]func(*sqlc.Queries) error{
		"item": func(q *sqlc.Queries) error {
			_, err := BindSession(q, tenant, session).PutItem(t.Context(), item.TurnID, time.Now(), items.Change{Item: item})
			return err
		},
		"change": func(q *sqlc.Queries) error {
			return AppendChanges(t.Context(), q, session, sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.item.added", Item: &item}})
		},
	} {
		err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error { return write(sqlc.New(tx)) })
		if !errors.Is(err, textvalue.ErrUnstorable) || pgunit.IsUnstorableText(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// A Turn that ends reports its finished Items in position order, then the Turn,
// then the settled Session activity, and its finished Items share one
// settlement time.
func TestTurnEndAppliesItemsTurnAndActivityInOrder(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, turn := newTurn(t, pool, sessions.TurnCompleted)
	turnID := uuid.UUID(turn.Bytes).String()
	created := time.Now().Add(-time.Minute)
	text := "partial"
	answer := v1.Item{ID: uuid.NewString(), TurnID: turnID, Type: "message", Role: "assistant", Status: "in_progress", Content: []v1.ItemContent{{Type: "output_text", Text: &text}}}
	search := v1.Item{ID: uuid.NewString(), TurnID: turnID, Type: "web_search_call", Status: "in_progress"}
	done := v1.Item{ID: uuid.NewString(), TurnID: turnID, Type: "web_search_call", Status: "completed"}
	inTx(t, pool, func(q *sqlc.Queries) error {
		for _, item := range []v1.Item{answer, search, done} {
			if _, err := BindSession(q, tenant, session).PutItem(t.Context(), turnID, created, items.Change{Item: item, Output: true}); err != nil {
				return err
			}
		}
		return nil
	})
	inTx(t, pool, func(q *sqlc.Queries) error {
		ending, err := LoadEnding(t.Context(), q, session, turn)
		if err != nil {
			return err
		}
		if len(ending.Unfinished) != 2 {
			t.Fatalf("unfinished %+v", ending.Unfinished)
		}
		ended := sessions.Turn{ID: turnID, SessionID: uuid.UUID(session.Bytes).String(), Status: sessions.TurnCompleted, CompletedAt: time.Now()}
		return ApplyTurnEnd(t.Context(), q, session, turn, sessions.EndTurn(ended, ending))
	})
	_, changes := journal(t, pool, session)
	var kinds []string
	for _, change := range changes {
		kinds = append(kinds, change.Event.Type)
	}
	want := []string{
		"agent.session.turn.output_text.done", "agent.session.turn.content_part.done", "agent.session.turn.item.done",
		"agent.session.turn.item.done", "agent.session.turn.completed", "agent.session.idle",
	}
	if !reflect.DeepEqual(kinds, want) || changes[2].Event.Item.ID != answer.ID || changes[3].Event.Item.ID != search.ID || !changes[5].Settled {
		t.Fatalf("journal %q", kinds)
	}
	rows, err := pool.Query(t.Context(), `SELECT id, payload->>'status', settled_at FROM session_items WHERE turn_id = $1`, turn)
	if err != nil {
		t.Fatal(err)
	}
	statuses, settled := map[string]string{}, map[string]time.Time{}
	for rows.Next() {
		var id uuid.UUID
		var status string
		var at time.Time
		if err := rows.Scan(&id, &status, &at); err != nil {
			t.Fatal(err)
		}
		statuses[id.String()], settled[id.String()] = status, at
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if statuses[answer.ID] != "incomplete" || statuses[search.ID] != "incomplete" || statuses[done.ID] != "completed" {
		t.Fatalf("statuses %v", statuses)
	}
	if !settled[answer.ID].Equal(settled[search.ID]) || !settled[answer.ID].After(created) || !settled[done.ID].Equal(created.Truncate(time.Microsecond)) {
		t.Fatalf("settlement times %v", settled)
	}
}
