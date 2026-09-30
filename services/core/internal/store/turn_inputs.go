package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// SubmitMessage and RequestCancel use the same request-level admission as batches.
func (s *Store) SubmitMessage(ctx context.Context, tenantID, sessionID, key string, payload json.RawMessage) (sessions.InputReceipt, error) {
	return s.submitOne(ctx, tenantID, sessionID, key, sessions.Input{Kind: "message", Payload: payload})
}

func (s *Store) RequestCancel(ctx context.Context, tenantID, sessionID, key string) (sessions.InputReceipt, error) {
	return s.submitOne(ctx, tenantID, sessionID, key, sessions.Input{Kind: "cancel", Payload: json.RawMessage(`{}`)})
}

func (s *Store) submitOne(ctx context.Context, tenantID, sessionID, key string, input sessions.Input) (sessions.InputReceipt, error) {
	receipts, err := s.SubmitInputs(ctx, tenantID, sessionID, key, []sessions.Input{input})
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	return receipts[0], nil
}

// SubmitInputs commits a request in order under one Session lock. The entire
// batch is the retry identity; replay never re-evaluates a cancellation target.
// Internal receipts are not the response body of the public events endpoint.
func (s *Store) SubmitInputs(ctx context.Context, tenantID, sessionID, key string, inputs []sessions.Input) ([]sessions.InputReceipt, error) {
	if err := sessions.ValidateInputKey(key); err != nil {
		return nil, err
	}
	batch, encoded, err := validateInputs(inputs)
	if err != nil {
		return nil, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	receipts := make([]sessions.InputReceipt, 0, len(batch))
	err = s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		previous, err := inputBatchReceipts(ctx, q, session, key, encoded)
		if err != nil {
			return err
		}
		if len(previous) > 0 {
			receipts = previous
			return auditpg.RecordWriteAudit(ctx, q, tenantID, "send_events", "session", uuid.UUID(session.Bytes).String(), "")
		}
		if slices.ContainsFunc(batch, func(input sessions.Input) bool { return input.Kind == "message" }) {
			if err := sessions.CheckFileWriteGate(ctx, sessionpg.BindSession(q, tenant, session)); err != nil {
				return err
			}
		}
		if err := checkEnvironmentInputGate(ctx, q, session, key, encoded); err != nil {
			return err
		}
		for position, input := range batch {
			receipt, err := admitInput(ctx, q, tenantID, session, key, int32(position), input)
			if err != nil {
				return err
			}
			receipts = append(receipts, receipt)
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "send_events", "session", uuid.UUID(session.Bytes).String(), "")
	})
	if err != nil {
		return nil, fmt.Errorf("submit turn inputs: %w", err)
	}
	return receipts, nil
}

func inputBatchReceipts(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, key string, batch json.RawMessage) ([]sessions.InputReceipt, error) {
	rows, err := q.FindInputBatch(ctx, sqlc.FindInputBatchParams{SessionID: session, IdempotencyKey: key, Batch: batch})
	if err != nil {
		return nil, err
	}
	receipts := make([]sessions.InputReceipt, 0, len(rows))
	for _, row := range rows {
		if !row.Matches {
			return nil, sessions.ErrIdempotencyConflict
		}
		receipts = append(receipts, inputReceipt(row.Sequence, row.TurnID, true))
	}
	return receipts, nil
}

func validateInputs(inputs []sessions.Input) ([]sessions.Input, json.RawMessage, error) {
	if len(inputs) == 0 || len(inputs) > 64 {
		return nil, nil, fmt.Errorf("%w: input batch must contain 1..64 events", sessions.ErrInvalidInput)
	}
	batch := make([]sessions.Input, len(inputs))
	size := 0
	for i, input := range inputs {
		size += len(input.Payload)
		if size > 512*1024 || len(input.Payload) == 0 || (input.Kind != "message" && input.Kind != "cancel" && input.Kind != "tool_result") {
			return nil, nil, fmt.Errorf("%w: input payloads must be nonempty and total at most 512 KiB", sessions.ErrInvalidInput)
		}
		payload, err := jsonobject.Normalize(input.Payload)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
		}
		if input.Kind == "cancel" && string(payload) != "{}" {
			return nil, nil, fmt.Errorf("%w: cancel payload must be empty", sessions.ErrInvalidInput)
		}
		if input.Kind == "tool_result" {
			if _, err := functionInput(payload); err != nil {
				return nil, nil, err
			}
		}
		batch[i] = sessions.Input{Kind: input.Kind, Payload: payload}
	}
	encoded, err := json.Marshal(batch)
	return batch, encoded, err
}

func admitInput(ctx context.Context, q *sqlc.Queries, tenantID string, session pgtype.UUID, key string, position int32, input sessions.Input) (sessions.InputReceipt, error) {
	if input.Kind == "tool_result" {
		return admitFunctionResult(ctx, q, tenantID, session, key, position, input)
	}
	created := false
	turn, err := q.GetActiveTurn(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.Kind == "message" {
			turn, err = q.CreateTurn(ctx, sqlc.CreateTurnParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session})
			if err == nil {
				created = true
				err = sessionpg.AppendChanges(ctx, q, session, sessions.TurnChanges(sessionpg.TurnFromRow(turn), true)...)
			}
		} else {
			err = nil // Retain even an idle cancellation's retry identity.
		}
	}
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	sequence, err := q.CreateTurnInput(ctx, sqlc.CreateTurnInputParams{
		SessionID: session, TurnID: turn.ID, IdempotencyKey: key, Kind: input.Kind, Payload: input.Payload, BatchPosition: position,
	})
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	if input.Kind == "cancel" && turn.ID.Valid {
		tenant, err := parseID(tenantID)
		if err != nil {
			return sessions.InputReceipt{}, err
		}
		if err := sessions.CancelTurn(ctx, sessionpg.BindSession(q, tenant, session), sessionpg.TurnFromRow(turn)); err != nil {
			return sessions.InputReceipt{}, err
		}
	}
	if err := indexInput(ctx, q, session, sequence); err != nil {
		return sessions.InputReceipt{}, err
	}
	if created {
		// A new Turn publishes turn.created, then its user input Items, then the
		// Session activity, within this transaction.
		usage, err := sessionpg.LoadUsage(ctx, q, session)
		if err != nil {
			return sessions.InputReceipt{}, err
		}
		if err := sessionpg.AppendChanges(ctx, q, session, sessions.ActivityChange(sessionpg.TurnFromRow(turn), usage, nil)); err != nil {
			return sessions.InputReceipt{}, err
		}
	}
	return inputReceipt(sequence, turn.ID, false), nil
}

// ListTurnInputs is an internal ordered recovery query, not the public SSE stream.
func (s *Store) ListTurnInputs(ctx context.Context, tenantID, sessionID, turnID string, after int64, limit int) ([]sessions.TurnInput, error) {
	params, err := sessionpg.TurnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: nonnegative cursor and page size 1..100 required", sessions.ErrInvalidInput)
	}
	if _, err := s.GetTurn(ctx, tenantID, sessionID, turnID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListTurnInputs(ctx, sqlc.ListTurnInputsParams{
		TenantID: params.TenantID, SessionID: params.SessionID, TurnID: params.ID, Sequence: after, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list turn inputs: %w", err)
	}
	inputs := make([]sessions.TurnInput, 0, len(rows))
	for _, row := range rows {
		inputs = append(inputs, sessions.TurnInput{Sequence: row.Sequence, Kind: row.Kind, Payload: row.Payload, CreatedAt: row.CreatedAt.Time})
	}
	return inputs, nil
}

func inputReceipt(sequence int64, turn pgtype.UUID, replayed bool) sessions.InputReceipt {
	receipt := sessions.InputReceipt{Sequence: sequence, Replayed: replayed}
	if turn.Valid {
		receipt.TurnID = uuid.UUID(turn.Bytes).String()
	}
	return receipt
}
