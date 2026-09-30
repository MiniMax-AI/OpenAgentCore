package claudecode_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// askCounterMinter emits ask_001, ask_002, ... for deterministic
// envelope IDs in tests.
func askCounterMinter() func() string {
	var (
		mu sync.Mutex
		n  int
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("ask_%03d", n)
	}
}

// TestTranslateAskUserQuestionToolUsePathFallsThrough locks in the
// dedupe behaviour: claude-code under --permission-prompt-tool stdio
// emits the same AskUserQuestion as BOTH a tool_use frame AND a
// control_request "can_use_tool" frame. We intercept only the
// control_request path (translateControlRequest); the tool_use frame
// passes through as a regular TypeToolCall so the run timeline still
// shows the call but we don't double-render the card.
func TestTranslateAskUserQuestionToolUsePathFallsThrough(t *testing.T) {
	pending := claudecode.NewPendingTableForTest()
	askPending := claudecode.NewPendingAskTableForTest()
	tr := claudecode.NewTranslatorWithAskForTest("run_a", pending, askPending, counterMinter(), askCounterMinter())

	line := []byte(`{"type":"assistant","message":{"content":[
		{"type":"tool_use","id":"toolu_abc","name":"AskUserQuestion","input":{
			"questions":[{
				"header":"Confirm delete",
				"question":"Delete /tmp directory?",
				"multiSelect":false,
				"options":[
					{"label":"Confirm delete","description":"Run rm -rf /tmp"},
					{"label":"Cancel","description":"Do not run"}
				]
			}]
		}}
	]}}`)

	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(out.Envelopes))
	}
	env := out.Envelopes[0]
	if env.Type != proto.TypeToolCall {
		t.Fatalf("env.Type = %q, want TypeToolCall (tool_use path no longer intercepts AskUserQuestion)", env.Type)
	}
	if askPending.Len() != 0 {
		t.Errorf("askPending should be empty (tool_use path is a pass-through), got %d entries", askPending.Len())
	}
}

// TestTranslateAskUserQuestionMultiQuestionIntercepted locks in the
// new "questions length > 1 still goes through the ask flow" path —
// daemon now renders a single multi-question card instead of falling
// through to a plain tool_call frame.
func TestTranslateAskUserQuestionMultiQuestionIntercepted(t *testing.T) {
	pending := claudecode.NewPendingTableForTest()
	askPending := claudecode.NewPendingAskTableForTest()
	tr := claudecode.NewTranslatorWithAskForTest("run_m", pending, askPending, counterMinter(), askCounterMinter())

	line := []byte(`{"type":"control_request","request_id":"cc_multi","request":{
		"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{
			"questions":[
				{"header":"q1","question":"pick A or B","options":[{"label":"A"},{"label":"B"}]},
				{"header":"q2","question":"pick X or Y","options":[{"label":"X"},{"label":"Y"}]}
			]
		}
	}}`)

	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(out.Envelopes))
	}
	if out.Envelopes[0].Type != proto.TypePromptForUserChoice {
		t.Errorf("want TypePromptForUserChoice, got %q", out.Envelopes[0].Type)
	}
	var payload proto.PromptForUserChoicePayload
	if err := json.Unmarshal(out.Envelopes[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Questions) != 2 {
		t.Fatalf("Questions len = %d, want 2", len(payload.Questions))
	}
	if payload.Questions[0].Header != "q1" || payload.Questions[1].Header != "q2" {
		t.Errorf("headers mismatch: %+v", payload.Questions)
	}
	if askPending.Len() != 1 {
		t.Errorf("askPending should have one entry, got %d", askPending.Len())
	}
}

// TestPendingAskTableTakeIsAtomic locks in the Take contract:
// concurrent Take(askID) callers must see exactly one ok=true. This is
// the contract SubmitPromptForUserChoice relies on to make sure a
// timer-fired cancel and a server-delivered answer can't both write a
// control_response back into claude's stdin.
func TestPendingAskTableTakeIsAtomic(t *testing.T) {
	tbl := claudecode.NewPendingAskTableForTest()
	headerQs := []proto.PromptForUserChoiceQuestion{{Header: "header text", Question: "?", Options: []proto.PromptForUserChoiceOption{{Label: "a"}}}}
	emptyQs := []proto.PromptForUserChoiceQuestion{{Question: "?", Options: []proto.PromptForUserChoiceOption{{Label: "a"}}}}
	tbl.RecordControl("ask_1", "cc_aaa", headerQs)
	tbl.RecordControl("ask_2", "cc_bbb", emptyQs)

	if tbl.Len() != 2 {
		t.Fatalf("Len = %d, want 2", tbl.Len())
	}

	// First Take consumes the entry.
	e, ok := tbl.Take("ask_1")
	if !ok {
		t.Fatalf("Take(ask_1) ok=false")
	}
	if e.CCRequestID != "cc_aaa" || len(e.Questions) != 1 || e.Questions[0].Header != "header text" {
		t.Errorf("entry mismatch: %+v", e)
	}
	if _, ok := tbl.Take("ask_1"); ok {
		t.Errorf("Take(ask_1) twice both ok=true; want second take to lose")
	}
	if _, ok := tbl.Peek("ask_1"); ok {
		t.Errorf("Peek(ask_1) after Take still ok")
	}
	if tbl.Len() != 1 {
		t.Errorf("Len = %d, want 1", tbl.Len())
	}
}

// TestPendingAskTableTakeRace ensures two goroutines calling Take on
// the same askID see exactly one winner — the real-world race we care
// about is the AskTimeout watchdog firing the same instant the server
// delivers the human's answer.
func TestPendingAskTableTakeRace(t *testing.T) {
	qs := []proto.PromptForUserChoiceQuestion{{Header: "h", Question: "?", Options: []proto.PromptForUserChoiceOption{{Label: "a"}}}}
	for i := range 200 {
		tbl := claudecode.NewPendingAskTableForTest()
		tbl.RecordControl("ask_x", "cc_x", qs)

		var wg sync.WaitGroup
		var winners int32
		start := make(chan struct{})
		wg.Add(2)
		for range 2 {
			go func() {
				defer wg.Done()
				<-start
				if _, ok := tbl.Take("ask_x"); ok {
					atomic.AddInt32(&winners, 1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if winners != 1 {
			t.Fatalf("iter %d: winners = %d, want 1", i, winners)
		}
	}
}

// TestBuildAskUserControlResponseMultiSelect ensures multiple answers join
// into a single human-friendly answer string.
func TestBuildAskUserControlResponseMultiSelect(t *testing.T) {
	body, err := claudecode.BuildAskUserControlResponseForTest(
		claudecode.PendingAskEntry{CCRequestID: "cc_m", Questions: []proto.PromptForUserChoiceQuestion{{ID: "q0", Header: "Pick lens"}}},
		proto.PromptForUserChoiceDecisionPayload{QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: "q0", Answers: []string{"Safety", "Performance"}}}},
	)
	if err != nil {
		t.Fatalf("buildAskUserControlResponse: %v", err)
	}
	v := decodeAskControlResponse(t, body)
	if !strings.Contains(v.Response.Response.Message, `"answer":"Safety、Performance"`) {
		t.Errorf("multi-select join failed: %s", v.Response.Response.Message)
	}
}

// Duplicate display headers cannot change the identity or ordering of answers.
func TestBuildAskUserControlResponseDuplicateHeaders(t *testing.T) {
	// Two questions with IDENTICAL headers — the realistic shape when the
	// model omits header entirely and both fall back to "".
	body, err := claudecode.BuildAskUserControlResponseForTest(
		claudecode.PendingAskEntry{
			CCRequestID: "cc_pos",
			Questions: []proto.PromptForUserChoiceQuestion{
				{ID: "first", Header: "", Question: "q1"},
				{ID: "second", Header: "", Question: "q2"},
			},
		},
		proto.PromptForUserChoiceDecisionPayload{
			QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{
				{QuestionID: "second", Answers: []string{"B1"}},
				{QuestionID: "first", Answers: []string{"A1"}},
			},
		},
	)
	if err != nil {
		t.Fatalf("buildAskUserControlResponse: %v", err)
	}
	v := decodeAskControlResponse(t, body)
	text := v.Response.Response.Message
	// Both answers must round-trip; the bug we're locking in against was
	// "both questions end up with B1" because map["":B1] overwrote "":A1.
	if !strings.Contains(text, `"answer":"A1"`) {
		t.Errorf("question 0 answer (A1) missing: %s", text)
	}
	if !strings.Contains(text, `"answer":"B1"`) {
		t.Errorf("question 1 answer (B1) missing: %s", text)
	}
	// Lock in the order — A1 must come first.
	if strings.Index(text, `"answer":"A1"`) > strings.Index(text, `"answer":"B1"`) {
		t.Errorf("answers out of order (A1 must precede B1): %s", text)
	}
}

func TestBuildAskUserControlResponseMatchesStableQuestionIDs(t *testing.T) {
	body, err := claudecode.BuildAskUserControlResponseForTest(
		claudecode.PendingAskEntry{
			CCRequestID: "cc_ids",
			Questions: []proto.PromptForUserChoiceQuestion{
				{ID: "environment", Header: "Environment"},
				{ID: "checks", Header: "Checks"},
			},
		},
		proto.PromptForUserChoiceDecisionPayload{
			QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{
				{QuestionID: "checks", Answers: []string{"Unit", "Integration"}},
				{QuestionID: "environment", Answers: []string{"Staging"}},
			},
		},
	)
	if err != nil {
		t.Fatalf("buildAskUserControlResponse: %v", err)
	}
	v := decodeAskControlResponse(t, body)
	text := v.Response.Response.Message
	if !strings.Contains(text, `"answer":"Staging","header":"Environment"`) {
		t.Fatalf("stable environment answer missing: %s", text)
	}
	if !strings.Contains(text, `"answer":"Unit、Integration","header":"Checks"`) {
		t.Fatalf("stable multi-select answer missing: %s", text)
	}
}

// askControlResponse is the JSON shape we expect from the control_request
// writeback path. The SDK reads back {response.subtype, response.request_id,
// response.response.behavior, response.response.message}.
type askControlResponse struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Response  struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		} `json:"response"`
	} `json:"response"`
}

func decodeAskControlResponse(t *testing.T, raw []byte) askControlResponse {
	t.Helper()
	raw = []byte(strings.TrimSpace(string(raw)))
	var v askControlResponse
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode control_response: %v\nraw=%s", err, raw)
	}
	if v.Type != "control_response" {
		t.Errorf("Type = %q, want control_response", v.Type)
	}
	if v.Response.Subtype != "success" {
		t.Errorf("Response.Subtype = %q, want success", v.Response.Subtype)
	}
	return v
}

// TestTranslateControlRequestAskUserQuestionIntercepted locks in the
// control_request path: when claude-code runs with
// --permission-prompt-tool stdio it wraps AskUserQuestion as a
// can_use_tool permission check rather than a normal tool_use frame.
// The daemon must surface a TypePromptForUserChoice envelope (not a
// generic TypePermissionRequest) and record the CC request_id so the
// writeback can hit the control_response channel.
func TestTranslateControlRequestAskUserQuestionIntercepted(t *testing.T) {
	pending := claudecode.NewPendingTableForTest()
	askPending := claudecode.NewPendingAskTableForTest()
	tr := claudecode.NewTranslatorWithAskForTest("run_c", pending, askPending, counterMinter(), askCounterMinter())

	line := []byte(`{"type":"control_request","request_id":"cc_req_xyz","request":{
		"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{
			"questions":[{
				"header":"Confirm delete",
				"question":"Delete /tmp directory?",
				"multiSelect":false,
				"options":[{"label":"Confirm"},{"label":"Cancel"}]
			}]
		}
	}}`)

	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(out.Envelopes))
	}
	env := out.Envelopes[0]
	if env.Type != proto.TypePromptForUserChoice {
		t.Fatalf("env.Type = %q, want %q", env.Type, proto.TypePromptForUserChoice)
	}
	if env.ID != "run_c" {
		t.Errorf("env.ID = %q, want run_c (envelope id is run id)", env.ID)
	}

	var payload proto.PromptForUserChoicePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.AskID != "ask_001" {
		t.Errorf("payload.AskID = %q, want ask_001", payload.AskID)
	}

	entry, ok := askPending.Peek("ask_001")
	if !ok {
		t.Fatalf("ask_001 not recorded")
	}
	if entry.CCRequestID != "cc_req_xyz" {
		t.Errorf("entry.CCRequestID = %q, want cc_req_xyz", entry.CCRequestID)
	}
	// And: a normal control_request (non-AskUserQuestion) still falls
	// through to the legacy permission path.
	tr2 := claudecode.NewTranslatorWithAskForTest("run_c2", claudecode.NewPendingTableForTest(), claudecode.NewPendingAskTableForTest(), counterMinter(), askCounterMinter())
	bashLine := []byte(`{"type":"control_request","request_id":"cc_bash_1","request":{
		"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}
	}}`)
	out2, err := tr2.Translate(bashLine)
	if err != nil {
		t.Fatalf("Translate Bash: %v", err)
	}
	if len(out2.Envelopes) != 1 || out2.Envelopes[0].Type != proto.TypePermissionRequest {
		t.Fatalf("Bash control_request must still produce TypePermissionRequest, got %#v", out2.Envelopes)
	}
}

// TestBuildAskUserControlResponseSingleSelect locks in the wire shape
// of the control_response writeback: behavior=deny + message carries
// the JSON answer payload. deny is intentional — claude shouldn't try
// to actually invoke its own AskUserQuestion local handler; the
// message is what the SDK surfaces to the model as the tool_result.
func TestBuildAskUserControlResponseSingleSelect(t *testing.T) {
	body, err := claudecode.BuildAskUserControlResponseForTest(
		claudecode.PendingAskEntry{CCRequestID: "cc_req_xyz", Questions: []proto.PromptForUserChoiceQuestion{{ID: "q0", Header: "Confirm delete"}}},
		proto.PromptForUserChoiceDecisionPayload{QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: "q0", Answers: []string{"Confirm"}}}},
	)
	if err != nil {
		t.Fatalf("buildAskUserControlResponse: %v", err)
	}
	v := decodeAskControlResponse(t, body)
	if v.Response.RequestID != "cc_req_xyz" {
		t.Errorf("RequestID = %q, want cc_req_xyz", v.Response.RequestID)
	}
	if v.Response.Response.Behavior != "deny" {
		t.Errorf("Behavior = %q, want deny", v.Response.Response.Behavior)
	}
	if !strings.Contains(v.Response.Response.Message, `"answer":"Confirm"`) {
		t.Errorf("answer not encoded in message: %s", v.Response.Response.Message)
	}
	if !strings.Contains(v.Response.Response.Message, `"header":"Confirm delete"`) {
		t.Errorf("header not echoed: %s", v.Response.Response.Message)
	}
}

// TestBuildAskUserControlResponseTimeout: timeout must still produce
// behavior=deny (no retry) + the canned timeout sentence in message.
func TestBuildAskUserControlResponseTimeout(t *testing.T) {
	body, err := claudecode.BuildAskUserControlResponseForTest(
		claudecode.PendingAskEntry{CCRequestID: "cc_req_to", Questions: []proto.PromptForUserChoiceQuestion{{Header: "?"}}},
		proto.PromptForUserChoiceDecisionPayload{Cancelled: true, Reason: "timeout"},
	)
	if err != nil {
		t.Fatalf("buildAskUserControlResponse: %v", err)
	}
	v := decodeAskControlResponse(t, body)
	if v.Response.Response.Behavior != "deny" {
		t.Errorf("Behavior = %q, want deny", v.Response.Response.Behavior)
	}
	if !strings.Contains(v.Response.Response.Message, "10 minutes") {
		t.Errorf("timeout text missing window: %s", v.Response.Response.Message)
	}
}
