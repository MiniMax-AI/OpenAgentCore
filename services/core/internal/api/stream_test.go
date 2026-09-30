package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// streamFixture serves one Session and its event stream.
type streamFixture struct {
	session store.Session
	mu      sync.Mutex
	changes []store.SessionChange
	gap     bool
	cursors []int64
}

func (f *streamFixture) GetSession(_ context.Context, tenant, id string) (store.Session, error) {
	if tenant != f.session.TenantID || id != f.session.ID {
		return store.Session{}, store.ErrNotFound
	}
	return f.session, nil
}

func (f *streamFixture) SessionEventCursor(context.Context, string, string) (int64, error) {
	return 10, nil
}

func (f *streamFixture) SessionStreamSnapshot(_ context.Context, tenant, id string) (store.Session, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tenant != f.session.TenantID || id != f.session.ID {
		return store.Session{}, 0, store.ErrNotFound
	}
	return f.session, 10, nil
}

func (f *streamFixture) ListSessionEvents(_ context.Context, _, _ string, cursor int64) ([]store.SessionChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursors = append(f.cursors, cursor)
	if f.gap {
		return nil, store.ErrStreamGap
	}
	changes := f.changes
	f.changes = nil
	return changes, nil
}

// serve answers Session reads and the event stream from f.
func (f *streamFixture) serve(fakes *testFakes) {
	fakes.sessions.getSession = f.GetSession
	fakes.sessionEvents.sessionEventCursor, fakes.sessionEvents.sessionStreamSnapshot, fakes.sessionEvents.listSessionEvents = f.SessionEventCursor, f.SessionStreamSnapshot, f.ListSessionEvents
}

func TestLiveStreamAuthDisconnectRecoveryAndServerDeadline(t *testing.T) {
	f := &streamFixture{session: store.Session{ID: uuid.NewString(), TenantID: uuid.NewString(), CreatedAt: time.Now(), Metadata: map[string]string{},
		Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[]},"environment":{"type":"none"}}`)}}
	deps, fakes := testDependencies(t)
	fakes.projects.resolveProjectAPIKey = projectKeys(t, APIKey{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential("key"), TenantID: f.session.TenantID}, APIKey{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential("foreign"), TenantID: uuid.NewString()}).ResolveProjectAPIKey
	f.serve(fakes)
	h := newTestHandler(t, deps)
	done := make(chan struct{}, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done <- struct{}{} }()
		h.ServeHTTP(w, r)
	}))
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	request := func(key string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/agents/sessions/"+f.session.ID+"/events", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Last-Event-ID", "old-history")
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, pair := range []struct {
		key    string
		status int
	}{{"invalid", 401}, {"foreign", 404}} {
		response := request(pair.key)
		_ = response.Body.Close()
		if response.StatusCode != pair.status || response.Header.Get("Content-Type") == "text/event-stream" {
			t.Fatal("authentication happened after stream headers", response.StatusCode)
		}
		<-done
	}
	response := request("key")
	// SSE responses carry the request ID and processing time (HP-23/HP-24).
	if !requestIDPattern.MatchString(response.Header.Get("X-Request-Id")) || response.Header.Get("Openai-Processing-Ms") == "" {
		t.Fatal("stream headers", response.Header)
	}
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != ": connected\n" {
		t.Fatal(line, err)
	}
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	f.gap = true
	f.mu.Unlock()
	rest, err := io.ReadAll(reader)
	_ = response.Body.Close()
	if err != nil || !strings.Contains(string(rest), `"code":"stream_interrupted"`) || !strings.Contains(string(rest), "event: error") {
		t.Fatal("deadline or gap handling failed", string(rest), err)
	}
	<-done
	f.mu.Lock()
	f.gap = false
	cursors := append([]int64(nil), f.cursors...)
	f.mu.Unlock()
	if len(cursors) == 0 || cursors[0] != 10 {
		t.Fatal("stream replayed history", cursors)
	}
	response = request("key")
	_ = response.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not release the handler")
	}

	// An unread large event must hit a write deadline instead of retaining a handler indefinitely.
	response = request("key")
	f.mu.Lock()
	text := strings.Repeat("x", 16*1024*1024)
	f.changes = []store.SessionChange{{Sequence: 11, Event: v1.SessionEvent{Type: "agent.session.turn.output_text.delta", EventID: "large", SessionID: f.session.ID, Delta: &text}}}
	f.mu.Unlock()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Error("slow reader retained the handler")
	}
	_ = response.Body.Close()
}

func TestTerminalTurnEventsMirrorTurnUsage(t *testing.T) {
	session := store.Session{ID: "session", Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[]},"environment":{"type":"none"}}`)}
	measured := json.RawMessage(`{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}`)
	child := &v1.Turn{ID: "child", Status: "cancelled"}
	for _, test := range []struct {
		change store.SessionChange
		want   string
	}{
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.completed"}, Turn: &store.Turn{ID: "turn", Status: store.TurnCompleted, Usage: measured}}, string(measured)},
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.failed"}, Turn: &store.Turn{ID: "turn", Status: store.TurnFailed}}, "null"},
		// Child Turn snapshots are rendered when recorded.
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.cancelled", Turn: child}}, "null"},
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.in_progress"}, Turn: &store.Turn{ID: "turn", Status: store.TurnInProgress, Usage: measured}}, ""},
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.idle"}, Turn: &store.Turn{ID: "turn", Status: store.TurnCompleted, Usage: measured}, SessionUsage: measured}, ""},
	} {
		event, err := streamResponse(session, test.change, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		usage, present := fields["usage"]
		if present != (test.want != "") || (present && string(usage) != test.want) {
			t.Fatal("terminal usage does not mirror the Turn snapshot", test.change.Event.Type, string(raw))
		}
		if present {
			var turn map[string]json.RawMessage
			if json.Unmarshal(fields["turn"], &turn) != nil || string(turn["usage"]) != string(usage) {
				t.Fatal("top-level usage differs from the rendered Turn", string(raw))
			}
		}
	}
}

// Item events carry output_index, null for input Items, and Session snapshots
// carry both reasoning keys (EVT-09, SES-23).
func TestStreamEventsCarryExplicitNullFields(t *testing.T) {
	session := store.Session{ID: "session", Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[],"reasoning":{}},"environment":{"type":"none"}}`)}
	text := "question"
	user := &v1.Item{ID: "item", TurnID: "turn", Type: "message", Status: "completed", Role: "user", Content: []v1.ItemContent{{Type: "input_text", Text: &text}}}
	result := &v1.Item{ID: "result", TurnID: "turn", Type: "function_call_output", Status: "completed", CallID: "call", Output: "value"}
	index := int32(0)
	for _, test := range []struct {
		change store.SessionChange
		want   map[string]string
	}{
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.item.added", TurnID: "turn", Item: user}}, map[string]string{"output_index": "null"}},
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.item.added", TurnID: "turn", Item: result}}, map[string]string{"output_index": "null"}},
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.item.done", TurnID: "turn", OutputIndex: &index, Item: &v1.Item{ID: "answer", TurnID: "turn", Type: "message", Status: "completed", Role: "assistant", Content: []v1.ItemContent{{Type: "output_text", Text: &text}}}}}, map[string]string{"output_index": "0"}},
		{store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.idle"}, Turn: &store.Turn{ID: "turn", Status: store.TurnCompleted}}, map[string]string{"output_index": ""}},
	} {
		event, err := streamResponse(session, test.change, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for key, want := range test.want {
			if value, present := fields[key]; present != (want != "") || (present && string(value) != want) {
				t.Fatalf("%s: %s", key, raw)
			}
		}
		var item map[string]json.RawMessage
		_ = json.Unmarshal(fields["item"], &item)
		switch {
		case test.change.Event.Item == result:
			if string(item["output"]) != `"value"` || string(item["error"]) != "null" {
				t.Fatalf("function result fields: %s", raw)
			}
		case test.change.Event.Item != nil:
			if phase, present := item["phase"]; !present || string(phase) != "null" {
				t.Fatalf("message phase: %s", raw)
			}
		default:
			if !strings.Contains(string(fields["session"]), `"reasoning":{"effort":null,"summary":null}`) {
				t.Fatalf("session reasoning: %s", raw)
			}
		}
	}
}
