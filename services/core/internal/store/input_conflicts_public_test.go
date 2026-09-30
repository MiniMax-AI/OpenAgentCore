package store_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

const conflictAgent = `"agent":{"id":"agent_conflicts","model":"fixture","tools":[],"multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"}}`

// Official error bodies for Session input rejections (EVT-11, EVT-12, ERR-22,
// ERR-27). Every param is null and no message repeats caller input.
const (
	missingSessionBody = `{"error":{"message":"Resource not found.","type":"not_found_error","code":"not_found_error","param":null}}` + "\n"
	unknownCallBody    = `{"error":{"message":"Unknown pending tool call.","type":"invalid_request_error","code":"invalid_request_error","param":null}}` + "\n"
	otherTurnBody      = `{"error":{"message":"The tool call belongs to a different Turn.","type":"invalid_request_error","code":"invalid_request_error","param":null}}` + "\n"
	turnConflictBody   = `{"error":{"message":"The Turn cannot accept this input in its current state.","type":"conflict_error","code":"conflict_error","param":null}}` + "\n"
	pendingInputBody   = `{"error":{"message":"Earlier input to this Session is still pending.","type":"conflict_error","code":"conflict_error","param":null}}` + "\n"
	changedResultBody  = `{"error":{"message":"The tool call already has a different result.","type":"conflict_error","code":"conflict_error","param":null}}` + "\n"
	eventKeyReuseBody  = `{"error":{"message":"This idempotency key was used with different input.","type":"conflict_error","code":"idempotency_conflict","param":null}}` + "\n"
)

// TestSessionInputConflictsAndResultTargetsPostgres replays the Session input
// error rows CF2–CF4 and CF6–CF9 of the official semantics alignment over HTTP
// and PostgreSQL. Inside an owned Session, an unknown call
// or a call of another Turn is a 400 request error; missing, malformed and
// foreign Sessions stay the byte-identical 404. Conflicts use conflict_error,
// and every rejection leaves the database and the pending action unchanged.
func TestSessionInputConflictsAndResultTargetsPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	s, db := newManagedTestStoreDB(t)
	ctx := t.Context()
	tenant, owner, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "conflict-owner", TokenSHA256: runtimedevice.HashCredential(owner), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "conflict-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	h, err := publicHandler(t, s, db, auth, "codex", storeExecution(t, s), executorURL("https://executor.example"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}

	create := func(environment string, initial bool) string {
		t.Helper()
		input := sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
			Configuration: json.RawMessage(`{` + conflictAgent + `,"environment":` + environment + `}`)}
		if initial {
			input.InitialInputs = []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"reserved"}`)}}
		}
		session, err := s.CreateSession(ctx, tenant, input)
		if err != nil {
			t.Fatal(err)
		}
		return session.ID
	}
	// waiting starts a Turn that waits for one function result.
	waiting := func(session, key, call string) sessions.InputReceipt {
		t.Helper()
		receipt, err := s.SubmitMessage(ctx, tenant, session, key, json.RawMessage(`{"text":"work"}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.TransitionTurn(ctx, tenant, session, receipt.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordFunctionCall(ctx, tenant, session, receipt.TurnID, sessions.FunctionCall{CallID: call, ExecutorCallID: "native-" + call, Name: "lookup", Arguments: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	complete := func(session string, receipt sessions.InputReceipt, call string) {
		t.Helper()
		if err := s.ConfirmFunctionResult(ctx, tenant, session, receipt.TurnID, call); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CompleteExecution(ctx, tenant, session, receipt.TurnID, sessions.TurnCompleted, nil, "", receipt.Sequence); err != nil {
			t.Fatal(err)
		}
	}
	result := func(turn, call, output string) string {
		return `{"type":"agent.session.input.tool_result","turn_id":"` + turn + `","call_id":"` + call + `","success":true,"output":"` + output + `"}`
	}
	message := `{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"must roll back"}]}]}`
	submit := func(token, session, key string, events ...string) (int, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/agents/sessions/"+session+"/events", strings.NewReader(`{"events":[`+strings.Join(events, ",")+`]}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(raw)
	}
	read := func(session string) string {
		t.Helper()
		status, raw := client.do(owner, http.MethodGet, "/v1/agents/sessions/"+session, "", nil)
		if status != http.StatusOK {
			t.Fatal(status, raw)
		}
		return raw
	}
	// A rejection uses its own new Idempotency-Key unless key names one.
	type rejection struct {
		name, token, session string
		events               []string
		status               int
		body, key            string
	}
	// reject checks each response body and that no rejection wrote anything.
	reject := func(cases []rejection, watched ...string) {
		t.Helper()
		digest := databaseDigest(t, db.pool)
		before := make([]string, len(watched))
		for i, session := range watched {
			before[i] = read(session)
		}
		for _, tc := range cases {
			key := tc.key
			if key == "" {
				key = "rejected-" + tc.name
			}
			if status, body := submit(tc.token, tc.session, key, tc.events...); status != tc.status || body != tc.body {
				t.Errorf("%s: %d %s", tc.name, status, body)
			}
		}
		if after := databaseDigest(t, db.pool); !reflect.DeepEqual(after, digest) {
			t.Error("rejected input changed the database")
		}
		for i, session := range watched {
			if after := read(session); after != before[i] {
				t.Errorf("rejected input changed Session %s: %s != %s", session, before[i], after)
			}
		}
	}

	none := `{"type":"none"}`
	session := create(none, false)
	first := waiting(session, "first", "first-call")
	if err := s.SubmitFunctionResult(ctx, tenant, session, first.TurnID, "first-call", json.RawMessage(`{"success":true,"output":"one"}`)); err != nil {
		t.Fatal(err)
	}
	complete(session, first, "first-call")
	current := waiting(session, "second", "pending-call")
	other := create(none, false)
	otherTurn := waiting(other, "other", "other-call").TurnID
	var pending struct {
		Status          string `json:"status"`
		RequiredActions []struct {
			CallID string `json:"call_id"`
			TurnID string `json:"turn_id"`
		} `json:"required_actions"`
	}
	if err := json.Unmarshal([]byte(read(session)), &pending); err != nil || pending.Status != "requires_action" ||
		len(pending.RequiredActions) != 1 || pending.RequiredActions[0].CallID != "pending-call" || pending.RequiredActions[0].TurnID != current.TurnID {
		t.Fatal("fixture pending action", pending, err)
	}

	// CF6–CF8: result targets in owned, other owned, foreign and missing Sessions.
	valid := result(current.TurnID, "pending-call", "value")
	reject([]rejection{
		{"unknown-call", owner, session, []string{result(current.TurnID, "absent-call", "x")}, 400, unknownCallBody, ""},
		{"unknown-call-any-turn", owner, session, []string{result(uuid.NewString(), "absent-call", "x")}, 400, unknownCallBody, ""},
		{"unknown-call-malformed-turn", owner, session, []string{result("turn_malformed", "absent-call", "x")}, 400, unknownCallBody, ""},
		{"call-of-other-session", owner, other, []string{valid}, 400, unknownCallBody, ""},
		{"rollback-batch", owner, session, []string{message, valid, result(current.TurnID, "absent-call", "x")}, 400, unknownCallBody, ""},
		{"completed-turn", owner, session, []string{result(first.TurnID, "pending-call", "x")}, 400, otherTurnBody, ""},
		{"call-of-completed-turn", owner, session, []string{result(current.TurnID, "first-call", "one")}, 400, otherTurnBody, ""},
		{"unknown-turn", owner, session, []string{result(uuid.NewString(), "pending-call", "x")}, 400, otherTurnBody, ""},
		{"malformed-turn", owner, session, []string{message, result("turn_malformed", "pending-call", "x")}, 400, otherTurnBody, ""},
		{"other-session-turn", owner, session, []string{result(otherTurn, "pending-call", "x")}, 400, otherTurnBody, ""},
		{"foreign", foreign, session, []string{valid}, 404, missingSessionBody, ""},
		{"foreign-unknown-call", foreign, session, []string{result(current.TurnID, "absent-call", "x")}, 404, missingSessionBody, ""},
		{"foreign-malformed-turn", foreign, session, []string{result("turn_malformed", "pending-call", "x")}, 404, missingSessionBody, ""},
		{"foreign-cancel", foreign, session, []string{`{"type":"agent.session.input.cancel"}`}, 404, missingSessionBody, ""},
		{"missing", owner, uuid.NewString(), []string{valid}, 404, missingSessionBody, ""},
		{"malformed-session", owner, "sess_malformed", []string{result("turn_malformed", "absent-call", "x")}, 404, missingSessionBody, ""},
	}, session, other)

	// CF2: input while earlier Session input waits for admission.
	awaiting := create(`{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}`, true)
	reject([]rejection{
		{"pending-cancel", owner, awaiting, []string{`{"type":"agent.session.input.cancel"}`}, 409, pendingInputBody, ""},
		{"pending-message", owner, awaiting, []string{message}, 409, pendingInputBody, ""},
		{"pending-result", owner, awaiting, []string{valid}, 409, pendingInputBody, ""},
		{"pending-foreign", foreign, awaiting, []string{`{"type":"agent.session.input.cancel"}`}, 404, missingSessionBody, ""},
	}, awaiting)

	// CF9: the result and its identical retries are accepted.
	for _, key := range []string{"result", "result", "same-result"} {
		if status, body := submit(owner, session, key, valid); status != http.StatusAccepted || body != "" {
			t.Fatalf("%s: %d %s", key, status, body)
		}
	}
	// CF3 and CF4: a changed result conflicts; reusing a key with another batch
	// keeps Core's local Idempotency-Key code.
	changed := result(current.TurnID, "pending-call", "changed")
	reject([]rejection{
		{"changed-result", owner, session, []string{changed}, 409, changedResultBody, ""},
		{"changed-result-batch", owner, session, []string{message, changed}, 409, changedResultBody, ""},
		{"key-reuse", owner, session, []string{changed}, 409, eventKeyReuseBody, "result"},
		{"changed-foreign", foreign, session, []string{changed}, 404, missingSessionBody, ""},
	}, session)
	complete(session, current, "pending-call")
	reject([]rejection{
		{"changed-after-completion", owner, session, []string{changed}, 409, changedResultBody, ""},
		{"changed-after-completion-foreign", foreign, session, []string{changed}, 404, missingSessionBody, ""},
	}, session)
	if status, body := submit(owner, session, "same-after-completion", valid); status != http.StatusAccepted || body != "" {
		t.Fatalf("identical result after completion: %d %s", status, body)
	}
	if call, err := s.GetFunctionCall(ctx, tenant, session, current.TurnID, "pending-call"); err != nil || !bytes.Contains(call.Result, []byte(`"value"`)) {
		t.Fatal("saved result changed", call, err)
	}

	// CF2: a result after its Turn was cancelled.
	cancelled := waiting(session, "third", "late-call")
	if status, body := submit(owner, session, "cancel", `{"type":"agent.session.input.cancel"}`); status != http.StatusAccepted {
		t.Fatalf("cancel: %d %s", status, body)
	}
	late := result(cancelled.TurnID, "late-call", "late")
	reject([]rejection{
		{"after-cancel", owner, session, []string{late}, 409, turnConflictBody, ""},
		{"after-cancel-foreign", foreign, session, []string{late}, 404, missingSessionBody, ""},
	}, session)
	if call, err := s.GetFunctionCall(ctx, tenant, session, cancelled.TurnID, "late-call"); err != nil || call.Result != nil {
		t.Fatal("late result was saved", call, err)
	}

	// CF4: Session creation key reuse keeps its local code with the conflict type.
	createSession := func(input string) (int, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"conflict-model"},"environment":{"type":"none"},"input":"`+input+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+owner)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "creation")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(raw)
	}
	if status, body := createSession("first"); status != http.StatusCreated {
		t.Fatal(status, body)
	}
	digest := databaseDigest(t, db.pool)
	status, body := createSession("changed")
	var creation struct {
		Error struct {
			Type  string  `json:"type"`
			Code  string  `json:"code"`
			Param *string `json:"param"`
		} `json:"error"`
	}
	if status != http.StatusConflict || json.Unmarshal([]byte(body), &creation) != nil ||
		creation.Error.Type != "conflict_error" || creation.Error.Code != "idempotency_conflict" || creation.Error.Param != nil {
		t.Fatalf("creation key reuse: %d %s", status, body)
	}
	if after := databaseDigest(t, db.pool); !reflect.DeepEqual(after, digest) {
		t.Error("creation key reuse changed the database")
	}
}
