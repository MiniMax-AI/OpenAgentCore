package main

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// observeItems records the Turn's execution observations as the execution
// journal does, composing the journal procedure over a pooled Session
// transaction because the seeder holds no execution lease.
func observeItems(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID, turn, status string) error {
	events := []sessions.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"answer","delta":"partial answer"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"command","stage":"after","observation":{"status":"failed","kind":"command","command":"exit 7","cwd":"/workspace","output":"command failed","exit_code":7,"duration_ms":8}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"mcp","stage":"after","observation":{"status":"completed","kind":"mcp","server":"reference","name":"lookup","arguments":{"n":9007199254740993},"output":{"structuredContent":{"n":9007199254740993}},"error":null}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"dynamic","stage":"after","observation":{"status":"completed","kind":"function","name":"reference::lookup","arguments":{},"content":[{"type":"input_text","text":""}]}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"patch","stage":"after","observation":{"status":"completed","kind":"function","name":"apply_patch","arguments":{"changes":[{"path":"/workspace/sample","diff":"+example"}]}}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"search","stage":"after","observation":{"status":"completed","kind":"web_search","action":{"type":"search","query":"reference"}}}`)},
	}
	if status == sessions.TurnCompleted || status == sessions.TurnFailed {
		events = append(events, sessions.ExecutionEvent{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"final answer","phase":"final_answer"}`)})
	}
	batch, err := sessions.NewJournalBatch(turn, 1, events)
	if err != nil {
		return err
	}
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return err
	}
	session, err := pgunit.ParseID(sessionID)
	if err != nil {
		return err
	}
	return sessionpg.WithSession(ctx, pgunit.NewPool(pool), tenant, session, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		return sessions.AppendTurnEvents(ctx, sessionpg.BindSession(q, tenant, session), batch)
	})
}
