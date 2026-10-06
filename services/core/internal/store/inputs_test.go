package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var messagePayload = json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)

func messageInput(text string) sessions.Input {
	payload, _ := json.Marshal(map[string]string{"text": text})
	return sessions.Input{Kind: "message", Payload: payload}
}

func newTurnSession(t *testing.T, s *Store) (string, sessions.Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(context.Background(), tenant, sessions.CreateSession{Creator: FixtureCreator(),
		Engine: "codex", IdempotencyKey: "session", Configuration: json.RawMessage(`{"agent":{"model":"test","instructions":"original"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session
}

func environmentInputSession(t *testing.T, s *Store) (string, sessions.Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(context.Background(), tenant, environmentInput("session", "self_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session
}

// submitInputs admits inputs through the Session service on s's database, as
// the public events route does.
func submitInputs(ctx context.Context, s *Store, tenant, session, key string, inputs []sessions.Input) ([]sessions.InputReceipt, error) {
	service, err := sessions.NewService(sessionAdapter(s))
	if err != nil {
		return nil, err
	}
	return service.SubmitInputs(ctx, tenant, session, key, inputs)
}

func submitInput(ctx context.Context, s *Store, tenant, session, key string, input sessions.Input) (sessions.InputReceipt, error) {
	receipts, err := submitInputs(ctx, s, tenant, session, key, []sessions.Input{input})
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	return receipts[0], nil
}

func sendMessage(ctx context.Context, s *Store, tenant, session, key string, payload json.RawMessage) (sessions.InputReceipt, error) {
	return submitInput(ctx, s, tenant, session, key, sessions.Input{Kind: "message", Payload: payload})
}

func requestCancel(ctx context.Context, s *Store, tenant, session, key string) (sessions.InputReceipt, error) {
	return submitInput(ctx, s, tenant, session, key, sessions.Input{Kind: "cancel", Payload: json.RawMessage(`{}`)})
}

func submitMessage(t *testing.T, s *Store, tenant, session, key string) sessions.InputReceipt {
	t.Helper()
	receipt, err := sendMessage(context.Background(), s, tenant, session, key, messagePayload)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func reserveEnvironmentInput(t *testing.T, s *Store, tenant, session, key string) sessions.EnvironmentInputReservation {
	t.Helper()
	got, err := sessionService(t, s).ReserveEnvironmentInput(context.Background(), tenant, session, key, []sessions.Input{messageInput("first"), messageInput("second")})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// cancelEnvironmentInput cancels the Session's pending Environment input as
// Session cancellation does, then reads the reservation back.
func cancelEnvironmentInput(ctx context.Context, s *Store, tenant, session, reservation string) (sessions.EnvironmentInputReservation, error) {
	owner, err := parseID(tenant)
	if err != nil {
		return sessions.EnvironmentInputReservation{}, err
	}
	err = s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, id pgtype.UUID) error {
		bound := sessionpg.BindSession(q, owner, id)
		return sessions.TrackInputActivity(ctx, bound, func(ctx context.Context) error { return bound.CancelPendingInput(ctx) })
	})
	if err != nil {
		return sessions.EnvironmentInputReservation{}, err
	}
	return sessionAdapter(s).GetEnvironmentInputReservation(ctx, tenant, session, reservation)
}

func transition(t *testing.T, s *Store, tenant, session, turn, from, to string) sessions.Turn {
	t.Helper()
	got, err := transitionTurn(context.Background(), s, tenant, session, turn, sessions.TurnTransition{ExpectedStatus: from, Status: to})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func environmentInputHistory(t *testing.T, pool *pgxpool.Pool, session string, turns, inputs int) {
	t.Helper()
	var gotTurns, gotInputs, items, events int
	err := pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM turns WHERE session_id=$1),
		       (SELECT count(*) FROM turn_inputs WHERE session_id=$1),
		       (SELECT count(*) FROM session_items WHERE session_id=$1),
		       (SELECT count(*) FROM session_events WHERE session_id=$1
		        AND (payload ? 'turn' OR payload->'event' ? 'item'))`, session).Scan(&gotTurns, &gotInputs, &items, &events)
	if err != nil || gotTurns != turns || gotInputs != inputs || items != inputs || (inputs == 0 && events != 0) {
		t.Fatal("history", gotTurns, gotInputs, items, events, err)
	}
}
