package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestItemsRecoverSnapshotsPartialResultsPaginationAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	journal := executionOwner(t, fixtureDB{pool: pool}, s).Sessions
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "items"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "first", json.RawMessage(`{"text":"question"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	batch := []sessions.ExecutionEvent{
		{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"in_progress"}`)},
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"answer","delta":"draft"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"cmd","stage":"before","observation":{"status":"in_progress","kind":"command","command":"exit 7"}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"cmd","stage":"after","observation":{"status":"failed","kind":"command","command":"exit 7","output":"expected failure","exit_code":7}}`)},
		{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"corrected answer","phase":"final_answer"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"mcp","stage":"after","observation":{"status":"completed","kind":"mcp","server":"reference","name":"lookup","arguments":{},"output":{"structuredContent":{"number":9007199254740993}}}}`)},
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"partial","delta":"unfinished"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"waiting","stage":"before","observation":{"status":"in_progress","kind":"command","command":"sleep 10"}}`)},
		{Kind: "done", Payload: json.RawMessage(`{"content":"corrected answer","metadata":{"agent_session_id":"PRIVATE"}}`)},
	}
	for range 2 {
		if err = journal.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
			t.Fatal(err)
		}
	}
	page, err := sessionReads(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 6 {
		t.Fatalf("wrong count: %+v", page)
	}
	if page.Items[4].Status != "in_progress" || page.Items[5].Status != "in_progress" {
		t.Fatal(page.Items)
	}
	_, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, sessions.TurnCancelled, json.RawMessage(`{}`), "", input.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	reopened := sessionReads(pool)
	page, err = reopened.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"completed", "completed", "failed", "completed", "incomplete", "incomplete"}
	for i, item := range page.Items {
		if item.Status != want[i] {
			t.Fatalf("item %d: %+v", i, item)
		}
	}
	if *page.Items[1].Content[0].Text != "corrected answer" || *page.Items[2].ExitCode != 7 {
		t.Fatal(page.Items)
	}
	raw, _ := json.Marshal(page.Items)
	if strings.Contains(string(raw), "PRIVATE") || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal(string(raw))
	}
	for _, asc := range []bool{true, false} {
		var all []v1.Item
		cursor := ""
		for {
			next, err := reopened.ListItems(ctx, tenant, session.ID, cursor, 2, asc)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, next.Items...)
			if !next.HasMore {
				break
			}
			cursor = next.Items[len(next.Items)-1].ID
		}
		for i, item := range all {
			j := i
			if !asc {
				j = len(all) - 1 - i
			}
			if !reflect.DeepEqual(item, page.Items[j]) {
				t.Fatal("pagination changed items")
			}
		}
	}
	other, _ := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "other"})
	// A foreign parent is not found before the cursor is read.
	if _, err = sessionReads(pool).ListItems(ctx, uuid.NewString(), session.ID, page.Items[0].ID, 20, true); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal(err)
	}
	// Another Session's Item is an invalid cursor here, like a missing or malformed one.
	for _, cursor := range []string{page.Items[0].ID, uuid.NewString(), "not-a-uuid"} {
		var invalid *sessions.CursorError
		if _, err = sessionReads(pool).ListItems(ctx, tenant, other.ID, cursor, 20, true); !errors.As(err, &invalid) || invalid.Message != "Invalid session item ID in `after`" {
			t.Fatal(cursor, err)
		}
	}
}

func TestItemProjectionFailureRollsBackJournalAndAggregateRecovers(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	journal := executionOwner(t, fixtureDB{pool: pool}, s).Sessions
	tenant := uuid.NewString()
	session, _ := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "legacy"})
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "input", json.RawMessage(`{"text":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	bad := []sessions.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"must roll back"}`)}, {Kind: "tool_call", Payload: json.RawMessage(`{"id":"mismatch","stage":"after","observation":{"status":"completed","kind":"invalid"}}`)}}
	if err = journal.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, bad); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	events, err := s.ListTurnEvents(ctx, tenant, session.ID, input.TurnID, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
	page, err := sessionReads(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnCompleted, Outcome: json.RawMessage(`{"done":{"content":"legacy answer","metadata":{"private":"SECRET"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	current, err := sessionReads(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(current.Items) != 2 || *current.Items[1].Content[0].Text != "legacy answer" {
		t.Fatal(current, err)
	}
}

func TestReceiptOnlyTextRecoversWithoutInventingCompletion(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	journal := executionOwner(t, fixtureDB{pool: pool}, s).Sessions
	tenant := uuid.NewString()
	for _, receiptOnly := range []bool{true, false} {
		session, _ := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString()})
		input, err := s.SubmitMessage(ctx, tenant, session.ID, "first", json.RawMessage(`{"text":"test"}`))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
		if err != nil {
			t.Fatal(err)
		}
		if receiptOnly {
			err = journal.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, []sessions.ExecutionEvent{{Kind: "cancel_receipt", Payload: json.RawMessage(`{"applied":true,"outcome":{"content":"retained cancellation text"}}`)}})
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, sessions.TurnCancelled, json.RawMessage(`{"done":{"content":"retained cancellation text"}}`), "", input.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		page, err := sessionReads(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
		if err != nil || len(page.Items) != 2 || page.Items[1].Status != "incomplete" || *page.Items[1].Content[0].Text != "retained cancellation text" {
			t.Fatal(page, err)
		}
	}
}

func TestLegacyFailureRetainsPartialAnswerAcrossRecovery(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	journal := executionOwner(t, fixtureDB{pool: pool}, s).Sessions
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "failed-items"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "first", json.RawMessage(`{"text":"question"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	batch := []sessions.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"delta":"partial answer"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"open","stage":"after","observation":{"status":"completed","kind":"web_search","action":{"type":"open_page","url":"https://example.com"}}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"find","stage":"after","observation":{"status":"completed","kind":"web_search","action":{"type":"find_in_page","url":"https://example.com","pattern":"needle"}}}`)},
		{Kind: "error", Payload: json.RawMessage(`{"error":"provider failure"}`)},
		{Kind: "done", Payload: json.RawMessage(`{"content":"provider failure"}`)},
	}
	if err = journal.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
		t.Fatal(err)
	}
	_, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, sessions.TurnFailed, json.RawMessage(`{"done":{"content":"provider failure"},"error_code":"engine_failed"}`), "", input.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	page, err := sessionReads(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 4 {
		t.Fatal(page, err)
	}
	if page.Items[1].Status != "incomplete" || *page.Items[1].Content[0].Text != "partial answer" || page.Items[2].Action.Type != "open_page" || page.Items[3].Action.Type != "find_in_page" {
		t.Fatal(page)
	}
}
