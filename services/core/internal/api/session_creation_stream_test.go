package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// creationStreamFixture serves one Session through both creation paths: the
// execution upsert (CreateSessionStream) and recorded-intent retry lookup. Like
// the Store, it commits events with ordered sequences together with the state
// they describe.
type creationStreamFixture struct {
	streamFixture
	creation  sessions.Creation
	recorded  bool
	deleted   bool
	sequence  int64
	snapshots int
	// beforeSnapshot and afterSnapshot commit racing work around the next read.
	beforeSnapshot, afterSnapshot func()
}

// GetSession returns the committed projection that commit changes atomically
// with its events.
func (f *creationStreamFixture) GetSession(_ context.Context, tenant, id string) (sessions.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleted || tenant != f.session.TenantID || id != f.session.ID {
		return sessions.Session{}, sessions.ErrNotFound
	}
	return f.session, nil
}

func (f *creationStreamFixture) SessionEventCursor(context.Context, string, string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sequence, nil
}

func (f *creationStreamFixture) ListSessionEvents(_ context.Context, _, _ string, cursor int64) ([]sessions.SessionChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursors = append(f.cursors, cursor)
	var changes []sessions.SessionChange
	for _, change := range f.changes {
		if change.Sequence > cursor {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

// SessionStreamSnapshot returns the projection and cursor from one snapshot,
// running the one-shot race hooks before and after that read.
func (f *creationStreamFixture) SessionStreamSnapshot(_ context.Context, tenant, id string) (sessions.Session, int64, error) {
	f.mu.Lock()
	before := f.beforeSnapshot
	f.beforeSnapshot = nil
	f.mu.Unlock()
	if before != nil {
		before()
	}
	f.mu.Lock()
	f.snapshots++
	session, cursor, deleted, race := f.session, f.sequence, f.deleted, f.afterSnapshot
	f.afterSnapshot = nil
	f.mu.Unlock()
	if race != nil {
		race()
	}
	if deleted || tenant != session.TenantID || id != session.ID {
		return sessions.Session{}, 0, sessions.ErrNotFound
	}
	return session, cursor, nil
}

func (f *creationStreamFixture) FindSessionCreation(context.Context, string, string, json.RawMessage, identity.Subject) (sessions.Creation, error) {
	if !f.recorded {
		return sessions.Creation{}, sessions.ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Recorded-intent lookup returns only the resource row and its cursor.
	row := f.session
	row.LastTurn, row.Usage, row.RequiredActions = nil, nil, nil
	return sessions.Creation{Session: row, Cursor: 10}, nil
}

// AuditSessionOperation accepts the audit of a replayed creation.
func (f *creationStreamFixture) AuditSessionOperation(context.Context, string, string, string) error {
	return nil
}

// CreateSessionStream is the Worker's streamed admission: it returns the
// prepared creation.
func (f *creationStreamFixture) CreateSessionStream(context.Context, string, sessions.CreateSession) (sessions.Creation, error) {
	return f.creation, nil
}

type sseFrame struct {
	Type string
	Data map[string]json.RawMessage
}

func (f sseFrame) event(t *testing.T) v1.SessionEvent {
	t.Helper()
	raw, _ := json.Marshal(f.Data)
	var event v1.SessionEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	return event
}

type creationStreamHarness struct {
	t       *testing.T
	fixture *creationStreamFixture
	server  *httptest.Server
	active  atomic.Int32
}

func newCreationStreamHarness(t *testing.T) *creationStreamHarness {
	t.Helper()
	tenant := uuid.NewString()
	fixture := &creationStreamFixture{sequence: 10, streamFixture: streamFixture{session: sessions.Session{
		ID: uuid.NewString(), TenantID: tenant, CreatedAt: time.Unix(1700000000, 0), Metadata: map[string]string{},
		Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[]},"environment":{"type":"none"}}`),
	}}}
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = projectKeys(t, APIKey{
		OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner",
		TokenSHA256: runtimedevice.HashCredential("key"), TenantID: tenant,
	}).ResolveAPIKey
	fakes.sessions.getSession, fakes.sessions.auditSessionOperation = fixture.GetSession, fixture.AuditSessionOperation
	fakes.sessionCreation.findSessionCreation = fixture.FindSessionCreation
	fakes.sessionEvents.sessionEventCursor, fakes.sessionEvents.sessionStreamSnapshot, fakes.sessionEvents.listSessionEvents = fixture.SessionEventCursor, fixture.SessionStreamSnapshot, fixture.ListSessionEvents
	fakes.modelProviders.resolve = noDeploymentModelProvider
	deps.Execution = fakes.execution()
	fakes.sessionAdmission.createSessionStream = fixture.CreateSessionStream
	handler := newTestHandler(t, deps)
	h := &creationStreamHarness{t: t, fixture: fixture}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.active.Add(1)
		defer h.active.Add(-1)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(h.server.Close)
	// Runs before server.Close: every stream handler must have returned.
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for h.active.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if h.active.Load() != 0 {
			t.Error("stream handler leaked after the stream ended or disconnected")
		}
	})
	return h
}

// turn returns a Turn snapshot with the given status for the fixture Session.
func (h *creationStreamHarness) turn(status string, usage string) *sessions.Turn {
	turn := &sessions.Turn{ID: "turn_1", SessionID: h.fixture.session.ID, Status: status, CreatedAt: time.Unix(1700000001, 0)}
	if usage != "" {
		turn.Usage = json.RawMessage(usage)
	}
	return turn
}

// commit applies a projection change and its events atomically, as the Store
// commits Session events with the state they describe.
func (h *creationStreamHarness) commit(update func(*sessions.Session), changes ...sessions.SessionChange) {
	h.fixture.mu.Lock()
	defer h.fixture.mu.Unlock()
	if update != nil {
		update(&h.fixture.session)
	}
	for _, change := range changes {
		h.fixture.sequence++
		change.Sequence = h.fixture.sequence
		h.fixture.changes = append(h.fixture.changes, change)
	}
}

func (h *creationStreamHarness) snapshots() int {
	h.fixture.mu.Lock()
	defer h.fixture.mu.Unlock()
	return h.fixture.snapshots
}

func (h *creationStreamHarness) open(method, path, body string) (<-chan sseFrame, func()) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(h.t.Context())
	request, err := http.NewRequestWithContext(ctx, method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "creation")
	response, err := h.server.Client().Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	want := http.StatusCreated
	if method == http.MethodGet {
		want = http.StatusOK
	}
	if response.StatusCode != want || response.Header.Get("Content-Type") != "text/event-stream" {
		h.t.Fatal("stream was not opened", response.StatusCode, response.Header)
	}
	frames := make(chan sseFrame, 64)
	go func() {
		defer close(frames)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		var frame sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			if value, ok := strings.CutPrefix(line, "event: "); ok {
				frame.Type = value
			} else if value, ok := strings.CutPrefix(line, "data: "); ok {
				_ = json.Unmarshal([]byte(value), &frame.Data)
			} else if line == "" && frame.Type != "" {
				frames <- frame
				frame = sseFrame{}
			}
		}
	}()
	return frames, cancel
}

func nextFrame(t *testing.T, frames <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("stream ended early")
		}
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("stream event timed out")
	}
	return sseFrame{}
}

func expectEnded(t *testing.T, frames <-chan sseFrame) {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if ok {
			t.Fatal("stream continued after settlement", frame.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end after settlement")
	}
}

// expectOpen outlasts the periodic projection re-read.
func expectOpen(t *testing.T, frames <-chan sseFrame) {
	t.Helper()
	select {
	case frame, ok := <-frames:
		t.Fatal("stream changed while it should wait", frame.Type, ok)
	case <-time.After(1200 * time.Millisecond):
	}
}

func expectTypes(t *testing.T, frames <-chan sseFrame, types ...string) []sseFrame {
	t.Helper()
	observed := make([]sseFrame, 0, len(types))
	for _, want := range types {
		frame := nextFrame(t, frames)
		if frame.Type != want || string(frame.Data["type"]) != `"`+want+`"` {
			t.Fatal("unexpected event order", frame.Type, "want", want)
		}
		observed = append(observed, frame)
	}
	return observed
}

func sameJSON(t *testing.T, raw json.RawMessage, value any) bool {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	if json.Unmarshal(raw, &left) != nil || json.Unmarshal(encoded, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func turnChange(kind string, turn *sessions.Turn) sessions.SessionChange {
	return sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn." + kind, EventID: uuid.NewString(), SessionID: turn.SessionID, TurnID: turn.ID}, Turn: turn}
}

// sessionChange records a Turn-owned Session status snapshot, settled when its
// Turn is terminal, as the Store records it.
func sessionChange(status string, turn *sessions.Turn, actions []v1.FunctionCallAction) sessions.SessionChange {
	settled := turn != nil && (turn.Status == sessions.TurnCompleted || turn.Status == sessions.TurnFailed || turn.Status == sessions.TurnCancelled)
	return sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session." + status, EventID: uuid.NewString(), SessionID: "session"}, Turn: turn, RequiredActions: actions, Settled: settled}
}

func activityChange(activity *sessions.EnvironmentInputActivity, settled bool) sessions.SessionChange {
	return sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session." + activity.Status, EventID: uuid.NewString()}, EnvironmentInputActivity: activity, Settled: settled}
}

func userItemChange(turn *sessions.Turn) sessions.SessionChange {
	text := "First"
	return sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.item.added", EventID: uuid.NewString(), SessionID: turn.SessionID, TurnID: turn.ID,
		Item: &v1.Item{ID: "item_1", TurnID: turn.ID, Type: "message", Status: "completed", Role: "user", Content: []v1.ItemContent{{Type: "input_text", Text: &text}}}}}
}

const measuredUsage = `{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}`

const creationBody = `{"agent":{"model":"model"},"environment":{"type":"none"},"input":"First","stream":true}`

const savedBody = `{"agent_id":"agent_test","environment":{"type":"none"},"input":"First","stream":true}`

func setTurn(turn *sessions.Turn) func(*sessions.Session) {
	return func(session *sessions.Session) { session.LastTurn, session.RequiredActions = turn, nil }
}

func TestCreationStreamClosesOnceSettled(t *testing.T) {
	for _, test := range []struct{ terminal, session, usage string }{
		{"completed", "idle", measuredUsage},
		{"cancelled", "idle", ""},
		{"failed", "failed", ""},
	} {
		t.Run(test.terminal, func(t *testing.T) {
			h := newCreationStreamHarness(t)
			// The admission result is the committed post-input projection.
			queued := h.turn(sessions.TurnQueued, "")
			h.commit(setTurn(queued), turnChange("created", queued), userItemChange(queued), sessionChange("in_progress", queued, nil))
			h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: 10}
			frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
			defer cancel()
			observed := expectTypes(t, frames, "agent.session.created", "agent.session.turn.created", "agent.session.turn.item.added", "agent.session.in_progress")
			created := observed[0].event(t)
			projection, err := sessionResponse(h.fixture.creation.Session, "")
			if err != nil || created.Session == nil || created.Session.Status != "in_progress" || !sameJSON(t, observed[0].Data["session"], projection) {
				t.Fatal("created snapshot differs from the JSON projection", string(observed[0].Data["session"]), err)
			}
			running := h.turn(sessions.TurnInProgress, "")
			h.commit(setTurn(running), turnChange("in_progress", running))
			expectTypes(t, frames, "agent.session.turn.in_progress")
			expectOpen(t, frames)

			settled := h.turn(test.terminal, test.usage)
			h.commit(setTurn(settled), turnChange(test.terminal, settled), sessionChange(test.session, settled, nil))
			terminal := expectTypes(t, frames, "agent.session.turn."+test.terminal, "agent.session."+test.session)
			expectEnded(t, frames)
			want := "null"
			if test.usage != "" {
				want = test.usage
			}
			var turn map[string]json.RawMessage
			if json.Unmarshal(terminal[0].Data["turn"], &turn) != nil || string(terminal[0].Data["usage"]) != want || string(turn["usage"]) != want {
				t.Fatal("terminal usage does not mirror the Turn snapshot", string(terminal[0].Data["usage"]), string(turn["usage"]))
			}
			for _, frame := range append(observed, terminal[1]) {
				if _, present := frame.Data["usage"]; present {
					t.Fatal("usage is limited to terminal Turn events", frame.Type)
				}
			}
		})
	}
}

func TestCreationStreamStaysOpenAcrossRequiredAction(t *testing.T) {
	h := newCreationStreamHarness(t)
	queued := h.turn(sessions.TurnQueued, "")
	h.commit(setTurn(queued), turnChange("created", queued), userItemChange(queued), sessionChange("in_progress", queued, nil))
	h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: 10}
	frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
	defer cancel()
	expectTypes(t, frames, "agent.session.created", "agent.session.turn.created", "agent.session.turn.item.added", "agent.session.in_progress")

	waiting := h.turn(sessions.TurnWaiting, "")
	actions := []v1.FunctionCallAction{{Type: "function_call", CallID: "call_1", Name: "lookup", TurnID: waiting.ID, Arguments: json.RawMessage(`{}`)}}
	h.commit(func(session *sessions.Session) { session.LastTurn, session.RequiredActions = waiting, actions },
		sessionChange("requires_action", waiting, actions))
	expectTypes(t, frames, "agent.session.requires_action")
	expectOpen(t, frames)

	// The function result resumes the Turn; the stream ends once it settles.
	resumed := h.turn(sessions.TurnInProgress, "")
	h.commit(setTurn(resumed), sessionChange("in_progress", resumed, nil))
	expectTypes(t, frames, "agent.session.in_progress")
	expectOpen(t, frames)
	completed := h.turn(sessions.TurnCompleted, measuredUsage)
	h.commit(setTurn(completed), turnChange("completed", completed), sessionChange("idle", completed, nil))
	expectTypes(t, frames, "agent.session.turn.completed", "agent.session.idle")
	expectEnded(t, frames)
}

func TestCreationStreamWithoutAdmittedWorkClosesAfterCreated(t *testing.T) {
	h := newCreationStreamHarness(t)
	h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: 10}
	// Work committed after the creation is not followed.
	queued := h.turn(sessions.TurnQueued, "")
	h.commit(nil, turnChange("created", queued))
	frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
	defer cancel()
	expectTypes(t, frames, "agent.session.created")
	expectEnded(t, frames)
	h.fixture.mu.Lock()
	defer h.fixture.mu.Unlock()
	if h.fixture.snapshots != 0 || len(h.fixture.cursors) != 0 {
		t.Fatal("a creation with nothing admitted read its projection or events", h.fixture.snapshots, h.fixture.cursors)
	}
}

func TestCreationStreamWaitsForPendingProvisioning(t *testing.T) {
	for _, outcome := range []string{"failure", "expiry without an event"} {
		t.Run(outcome, func(t *testing.T) {
			h := newCreationStreamHarness(t)
			// Hosted initial input is idle, without a Turn or public action, while it provisions.
			h.commit(func(session *sessions.Session) { session.PendingInput = true })
			h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: 10}
			frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
			defer cancel()
			if created := expectTypes(t, frames, "agent.session.created")[0].event(t); created.Session.Status != "idle" {
				t.Fatal("provisioning snapshot changed", created.Session.Status)
			}
			expectOpen(t, frames)
			if outcome == "failure" {
				failed := &sessions.EnvironmentInputActivity{Status: "failed", Failure: "environment_unavailable", LastActiveAt: time.Unix(1700000005, 0)}
				h.commit(func(session *sessions.Session) {
					session.PendingInput, session.EnvironmentInputActivity = false, failed
				},
					activityChange(failed, true))
				expectTypes(t, frames, "agent.session.failed")
			} else {
				// A reservation can settle without recording an event.
				h.commit(func(session *sessions.Session) { session.PendingInput = false })
			}
			expectEnded(t, frames)
		})
	}
}

func TestCreationStreamEndsWhenSessionIsDeleted(t *testing.T) {
	h := newCreationStreamHarness(t)
	queued := h.turn(sessions.TurnQueued, "")
	h.commit(setTurn(queued))
	h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: 10}
	frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
	defer cancel()
	expectTypes(t, frames, "agent.session.created")
	h.commit(func(*sessions.Session) { h.fixture.deleted = true })
	expectEnded(t, frames)
}

func TestGetStreamStaysOpenAfterSettlement(t *testing.T) {
	h := newCreationStreamHarness(t)
	h.commit(setTurn(h.turn(sessions.TurnInProgress, "")))
	frames, cancel := h.open(http.MethodGet, "/v1/agents/sessions/"+h.fixture.session.ID+"/events", "")
	defer cancel()
	completed := h.turn(sessions.TurnCompleted, "")
	h.commit(setTurn(completed), turnChange("completed", completed), sessionChange("idle", completed, nil))
	expectTypes(t, frames, "agent.session.turn.completed", "agent.session.idle")
	expectOpen(t, frames)
	later := h.turn(sessions.TurnQueued, "")
	failed := h.turn(sessions.TurnFailed, "")
	h.commit(setTurn(failed), turnChange("created", later), sessionChange("failed", failed, nil))
	expectTypes(t, frames, "agent.session.turn.created", "agent.session.failed")
	expectOpen(t, frames)
}

// The creation stream never runs into another client's Turn, whether that work
// commits in the same drained batch as the settling idle or between the
// fallback's settled snapshot and its drain.
func TestCreationStreamStopsBeforeLaterWork(t *testing.T) {
	later := func(h *creationStreamHarness) []sessions.SessionChange {
		queued := h.turn(sessions.TurnQueued, "")
		queued.ID = "turn_b"
		return []sessions.SessionChange{turnChange("created", queued), userItemChange(queued), sessionChange("in_progress", queued, nil)}
	}
	t.Run("same batch as the settling idle", func(t *testing.T) {
		h := newCreationStreamHarness(t)
		h.commit(setTurn(h.turn(sessions.TurnInProgress, "")))
		h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: h.fixture.sequence}
		frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
		defer cancel()
		expectTypes(t, frames, "agent.session.created")
		// The settling idle and B's new Turn commit before the next poll drains them together.
		completed := h.turn(sessions.TurnCompleted, "")
		changes := append([]sessions.SessionChange{turnChange("completed", completed), sessionChange("idle", completed, nil)}, later(h)...)
		h.commit(func(session *sessions.Session) { session.LastTurn = changes[2].Turn }, changes...)
		expectTypes(t, frames, "agent.session.turn.completed", "agent.session.idle")
		expectEnded(t, frames)
	})
	t.Run("between the settled snapshot and its drain", func(t *testing.T) {
		h := newCreationStreamHarness(t)
		h.commit(func(session *sessions.Session) { session.PendingInput = true })
		h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: h.fixture.sequence}
		frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
		defer cancel()
		expectTypes(t, frames, "agent.session.created")
		expectOpen(t, frames)
		h.fixture.mu.Lock()
		// After an empty drain, the reservation settles without a status event,
		// alongside an unsent environment event, before the fallback read.
		h.fixture.beforeSnapshot = func() {
			h.commit(func(session *sessions.Session) { session.PendingInput = false },
				sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.environment.disconnected", EventID: uuid.NewString(), SessionID: h.fixture.session.ID,
					Environment: &v1.SessionEnvironmentState{ID: "environment", Type: "self_hosted", Status: "disconnected"}}})
		}
		// B's Turn commits after that settled read and before its drain.
		h.fixture.afterSnapshot = func() {
			changes := later(h)
			h.commit(func(session *sessions.Session) { session.LastTurn = changes[0].Turn }, changes...)
		}
		h.fixture.mu.Unlock()
		expectTypes(t, frames, "agent.session.environment.disconnected")
		expectEnded(t, frames)
	})
}

func TestCreationStreamIgnoresConnectionIdle(t *testing.T) {
	h := newCreationStreamHarness(t)
	waiting := &sessions.EnvironmentInputActivity{Status: "requires_action", LastActiveAt: time.Unix(1700000002, 0)}
	h.commit(func(session *sessions.Session) { session.PendingInput = true })
	h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: h.fixture.sequence}
	frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
	defer cancel()
	expectTypes(t, frames, "agent.session.created")
	// A self-hosted connection clears the action; the input is still pending.
	connected := &sessions.EnvironmentInputActivity{Status: "idle", LastActiveAt: waiting.LastActiveAt}
	h.commit(func(session *sessions.Session) { session.EnvironmentInputActivity = connected }, activityChange(connected, false))
	expectTypes(t, frames, "agent.session.idle")
	expectOpen(t, frames)
	// Its later expiry records no event and ends the stream through the projection.
	h.commit(func(session *sessions.Session) { session.PendingInput = false })
	expectEnded(t, frames)
}

func TestCreationStreamBoundsProjectionReads(t *testing.T) {
	h := newCreationStreamHarness(t)
	running := h.turn(sessions.TurnInProgress, "")
	h.commit(setTurn(running))
	h.fixture.creation = sessions.Creation{Session: h.fixture.session, Created: true, Cursor: h.fixture.sequence}
	frames, cancel := h.open(http.MethodPost, "/v1/agents/sessions", creationBody)
	defer cancel()
	expectTypes(t, frames, "agent.session.created")
	start := h.snapshots()
	deadline := time.Now().Add(2200 * time.Millisecond)
	count := 0
	for time.Now().Before(deadline) {
		text := "x"
		h.commit(nil, sessions.SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn.output_text.delta", EventID: uuid.NewString(), SessionID: running.SessionID, TurnID: running.ID, Delta: &text}})
		count++
		time.Sleep(20 * time.Millisecond)
	}
	for range count {
		nextFrame(t, frames)
	}
	// Deltas carry no Session status, so reads stay on the once-a-second timer.
	if reads := h.snapshots() - start; reads > 4 {
		t.Fatal("chatty stream re-read its projection too often", reads, count)
	}
	h.commit(nil, sessionChange("in_progress", running, nil))
	expectTypes(t, frames, "agent.session.in_progress")
	expectOpen(t, frames)
}

// A same-key stream retry of an existing creation sends the connection comment
// and ends, whatever the Session is doing: nothing is replayed or followed.
func TestCreationRetryStreamEndsImmediately(t *testing.T) {
	for _, test := range []struct {
		name   string
		setup  func(h *creationStreamHarness)
		upsert bool
	}{
		{"initial work running", func(h *creationStreamHarness) {
			running := h.turn(sessions.TurnInProgress, "")
			h.commit(setTurn(running), turnChange("in_progress", running))
		}, false},
		{"initial work settled", func(h *creationStreamHarness) {
			completed := h.turn(sessions.TurnCompleted, "")
			h.commit(setTurn(completed), turnChange("completed", completed), sessionChange("idle", completed, nil))
		}, false},
		{"superseded by another client's Turn", func(h *creationStreamHarness) {
			later := h.turn(sessions.TurnInProgress, "")
			later.ID = "turn_b"
			h.commit(setTurn(later), turnChange("created", later), userItemChange(later), sessionChange("in_progress", later, nil))
		}, false},
		{"upsert retry", func(h *creationStreamHarness) {
			h.commit(setTurn(h.turn(sessions.TurnInProgress, "")))
			h.fixture.creation = sessions.Creation{Session: h.fixture.session, Cursor: 10}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newCreationStreamHarness(t)
			h.fixture.recorded = !test.upsert
			test.setup(h)
			body := savedBody
			if test.upsert {
				body = creationBody
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+"/v1/agents/sessions", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "creation")
			response, err := h.server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusCreated || response.Header.Get("Content-Type") != "text/event-stream" || string(raw) != ": connected\n\n" {
				t.Fatal("retry stream did not end at once", response.StatusCode, string(raw), err)
			}
			h.fixture.mu.Lock()
			defer h.fixture.mu.Unlock()
			if h.fixture.snapshots != 0 || len(h.fixture.cursors) != 0 {
				t.Fatal("retry stream read Session state or events", h.fixture.snapshots, h.fixture.cursors)
			}
		})
	}
}

func TestSettlingEvents(t *testing.T) {
	for _, test := range []struct {
		change  sessions.SessionChange
		settles bool
	}{
		{sessionChange("idle", &sessions.Turn{Status: sessions.TurnCompleted}, nil), true},
		{activityChange(&sessions.EnvironmentInputActivity{Status: "idle"}, true), true},
		{activityChange(&sessions.EnvironmentInputActivity{Status: "idle"}, false), false},
		{activityChange(&sessions.EnvironmentInputActivity{Status: "failed"}, true), true},
		{sessionChange("failed", &sessions.Turn{Status: sessions.TurnFailed}, nil), true},
		{sessionChange("requires_action", &sessions.Turn{Status: sessions.TurnWaiting}, nil), false},
		{sessionChange("in_progress", &sessions.Turn{Status: sessions.TurnQueued}, nil), false},
		{turnChange("completed", &sessions.Turn{Status: sessions.TurnCompleted}), false},
	} {
		if settlingEvent(test.change) != test.settles {
			t.Fatal("unexpected settling event", test.change.Event.Type, test.change.Settled)
		}
	}
}

func TestSessionSettledProjection(t *testing.T) {
	for _, test := range []struct {
		status  string
		turn    string
		pending bool
		settled bool
	}{
		{"idle", sessions.TurnCompleted, false, true},
		{"failed", sessions.TurnFailed, false, true},
		{"idle", "", false, true},
		{"failed", "", false, true},
		{"idle", "", true, false},
		{"idle", sessions.TurnCancelled, true, false},
		{"in_progress", sessions.TurnQueued, false, false},
		{"requires_action", sessions.TurnWaiting, false, false},
		{"requires_action", "", true, false},
	} {
		session := sessions.Session{PendingInput: test.pending}
		if test.turn != "" {
			session.LastTurn = &sessions.Turn{Status: test.turn}
		}
		if sessionSettled(session, v1.Session{Status: test.status}) != test.settled {
			t.Fatal("unexpected settlement", test)
		}
	}
}
