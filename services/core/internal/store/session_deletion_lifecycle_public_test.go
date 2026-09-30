package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

const deletionAgent = `"agent":{"id":"agent_deletion","model":"fixture","tools":[],"multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"}}`

// TestSessionDeletionLifecyclePostgres replays the official deletion lifecycle
// over HTTP and PostgreSQL: busy Sessions conflict without any database write,
// settled Sessions delete once and confirm again for their owner, and foreign,
// missing and malformed identifiers keep one not-found response.
func TestSessionDeletionLifecyclePostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	s, pool := store.NewManagedTestStore(t)
	ctx := t.Context()
	tenant, owner, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "deletion-owner", TokenSHA256: runtimedevice.HashCredential(owner), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "deletion-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	h, err := publicHandler(t, s, auth, "codex", storeExecution(t, s), executorURL("https://executor.example"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}
	writer, err := store.NewExecution(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = writer.CloseExecution(closing)
	})

	create := func(environment string, initial bool) store.Session {
		t.Helper()
		input := store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
			Configuration: json.RawMessage(`{` + deletionAgent + `,"environment":` + environment + `}`)}
		if initial {
			input.InitialInputs = []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"reserved"}`)}}
		}
		session, err := s.CreateSession(ctx, tenant, input)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	none := `{"type":"none"}`
	selfHosted := `{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}`
	hosted := `{"type":"openai_hosted","network":{"access":"disabled"}}`
	turn := func(to ...string) string {
		t.Helper()
		session := create(none, false)
		receipt, err := s.SubmitMessage(ctx, tenant, session.ID, "input", json.RawMessage(`{"text":"work"}`))
		if err != nil {
			t.Fatal(err)
		}
		from := store.TurnQueued
		for _, status := range to {
			switch status {
			case "cancel":
				_, err = s.RequestCancel(ctx, tenant, session.ID, "cancel")
			case "function":
				err = s.RecordFunctionCall(ctx, tenant, session.ID, receipt.TurnID, store.FunctionCall{CallID: "pending", ExecutorCallID: "native-pending", Name: "lookup", Arguments: json.RawMessage(`{}`)})
			case store.TurnCompleted, store.TurnFailed:
				_, err = s.CompleteExecution(ctx, tenant, session.ID, receipt.TurnID, status, nil, "", receipt.Sequence)
			default:
				_, err = s.TransitionTurn(ctx, tenant, session.ID, receipt.TurnID, store.TurnTransition{ExpectedStatus: from, Status: status})
				from = status
			}
			if err != nil {
				t.Fatal(status, err)
			}
		}
		return session.ID
	}
	reserve := func(session store.Session) store.EnvironmentInputReservation {
		t.Helper()
		reservation, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "later", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"later"}`)}})
		if err != nil || reservation.State != store.EnvironmentInputPending {
			t.Fatal(reservation, err)
		}
		return reservation
	}
	connect := func(session store.Session) {
		t.Helper()
		generation := uuid.NewString()
		if err := writer.ReplaceEnvironmentConnection(ctx, tenant, session.Environment.ID, generation); err != nil {
			t.Fatal(err)
		}
		if err := writer.ObserveEnvironmentConnection(ctx, tenant, session.Environment.ID, generation, 1, true); err != nil {
			t.Fatal(err)
		}
	}

	// D3: every Session that is not durably idle or failed without required actions.
	busy := map[string]string{
		"queued_turn":      turn(),
		"in_progress_turn": turn(store.TurnInProgress),
		"cancelling_turn":  turn(store.TurnInProgress, "cancel"),
		"required_action":  turn(store.TurnInProgress, "function"),
	}
	awaiting := create(selfHosted, true)
	busy["self_hosted_awaiting_connection"] = awaiting.ID
	queued := create(selfHosted, false)
	connect(queued)
	reserve(queued)
	busy["self_hosted_queued_input"] = queued.ID
	busy["hosted_provisioning_input"] = create(hosted, true).ID
	// Pending input can project idle while it still waits for admission.
	projected := map[string]string{
		"queued_turn": "in_progress", "in_progress_turn": "in_progress", "cancelling_turn": "in_progress",
		"required_action": "requires_action", "self_hosted_awaiting_connection": "requires_action",
		"self_hosted_queued_input": "idle", "hosted_provisioning_input": "idle",
	}

	// D4: settled Sessions, including idle hosted provisioning without input.
	settled := map[string]string{
		"none_idle":                 create(none, false).ID,
		"completed_turn":            turn(store.TurnInProgress, store.TurnCompleted),
		"failed_turn":               turn(store.TurnInProgress, store.TurnFailed),
		"cancelled_turn":            turn("cancel"),
		"self_hosted_idle":          create(selfHosted, false).ID,
		"hosted_provisioning_idle":  create(hosted, false).ID,
		"self_hosted_input_expired": "",
		"later_input_cancelled":     "",
	}
	expired := create(selfHosted, true)
	if _, err := pool.Exec(ctx, "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE session_id=$1", expired.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := writer.ExpireEnvironmentInputs(ctx); err != nil || count != 1 {
		t.Fatal("initial input did not expire", count, err)
	}
	settled["self_hosted_input_expired"] = expired.ID
	withdrawn := create(selfHosted, false)
	if _, err := s.CancelEnvironmentInput(ctx, tenant, withdrawn.ID, reserve(withdrawn).ID); err != nil {
		t.Fatal(err)
	}
	settled["later_input_cancelled"] = withdrawn.ID

	sessionPath := func(id string) string { return "/v1/agents/sessions/" + id }
	decode := func(raw string) map[string]any {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(raw, err)
		}
		return value
	}
	missingStatus, missing := client.do(owner, http.MethodDelete, sessionPath(uuid.NewString()), "", nil)
	if missingStatus != http.StatusNotFound {
		t.Fatal(missingStatus, missing)
	}
	if !reflect.DeepEqual(decode(missing), map[string]any{"error": map[string]any{
		"type": "not_found_error", "code": "not_found_error", "message": "Resource not found.", "param": nil}}) {
		t.Fatal("missing Session body", missing)
	}
	notFound := func(token, method, path string) {
		t.Helper()
		if status, raw := client.do(token, method, path, "", nil); status != http.StatusNotFound || raw != missing {
			t.Fatalf("%s %s: %d %s", method, path, status, raw)
		}
	}
	notFound(owner, http.MethodDelete, sessionPath("sess_malformed"))
	conflict := map[string]any{"error": map[string]any{
		"type": "conflict_error", "code": "conflict_error", "param": nil,
		"message": "session must be durably idle or failed without required actions before deletion"}}

	for name, id := range busy {
		t.Run("conflict/"+name, func(t *testing.T) {
			readStatus, before := client.do(owner, http.MethodGet, sessionPath(id), "", nil)
			if readStatus != http.StatusOK || decode(before)["status"] != projected[name] {
				t.Fatal(readStatus, before)
			}
			digest := databaseDigest(t, pool)
			notFound(foreign, http.MethodDelete, sessionPath(id))
			status, raw := client.do(owner, http.MethodDelete, sessionPath(id), "", nil)
			if status != http.StatusConflict || !reflect.DeepEqual(decode(raw), conflict) {
				t.Fatalf("busy Session deletion: %d %s", status, raw)
			}
			if after := databaseDigest(t, pool); !reflect.DeepEqual(after, digest) {
				t.Fatal("rejected deletion changed the database")
			}
			if readStatus, after := client.do(owner, http.MethodGet, sessionPath(id), "", nil); readStatus != http.StatusOK || after != before {
				t.Fatal("rejected deletion changed the Session", before, after)
			}
		})
	}
	for name, id := range settled {
		t.Run("deleted/"+name, func(t *testing.T) {
			notFound(foreign, http.MethodDelete, sessionPath(id))
			status, first := client.do(owner, http.MethodDelete, sessionPath(id), "", nil)
			if status != http.StatusOK || !reflect.DeepEqual(decode(first), map[string]any{"id": id, "object": "agent.session.deleted", "deleted": true}) {
				t.Fatalf("settled Session deletion: %d %s", status, first)
			}
			digest := databaseDigest(t, pool)
			filter := store.WriteOperationFilter{ResourceType: "session", ResourceID: id, Limit: 100}
			beforeAudit, err := s.ListWriteOperations(ctx, tenant, filter)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if status, again := client.do(owner, http.MethodDelete, sessionPath(id), "", nil); status != http.StatusOK || again != first {
					t.Fatalf("repeated deletion: %d %s", status, again)
				}
			}
			afterAudit, err := s.ListWriteOperations(ctx, tenant, filter)
			if err != nil || len(afterAudit.Data) != len(beforeAudit.Data)+2 || !reflect.DeepEqual(afterAudit.Data[2:], beforeAudit.Data) {
				t.Fatal("repeated deletion must append exactly two operation records", err)
			}
			for _, operation := range afterAudit.Data[:2] {
				if operation.Action != "delete" || operation.APIKey.ID != uuid.NewSHA1(uuid.NameSpaceOID, []byte(runtimedevice.HashCredential(owner))).String() {
					t.Fatal("repeated deletion recorded the wrong operation or key")
				}
			}
			// Only the new operation records may differ. Ownership, Session state,
			// execution data and every public response remain unchanged.
			after := databaseDigest(t, pool)
			delete(after, "write_audit_operations")
			delete(digest, "write_audit_operations")
			if !reflect.DeepEqual(after, digest) {
				t.Fatal("repeated deletion changed business data or ownership")
			}
			notFound(owner, http.MethodGet, sessionPath(id))
			notFound(owner, http.MethodGet, sessionPath(id)+"/turns")
			notFound(foreign, http.MethodDelete, sessionPath(id))
		})
	}
}
