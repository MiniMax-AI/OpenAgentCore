package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type visibilityEvent struct {
	Type   string `json:"type"`
	TurnID string `json:"turn_id"`
	Turn   *struct {
		ID         string  `json:"id"`
		SubagentID *string `json:"subagent_id"`
	} `json:"turn"`
	Item *struct {
		TurnID string `json:"turn_id"`
		Type   string `json:"type"`
	} `json:"item"`
}

type eventCollector struct {
	// idle closes when agent.session.idle follows a Turn completion; done then
	// receives every data frame once the stream ends or is stopped.
	idle chan struct{}
	done chan []visibilityEvent
}

func collectEvents(t *testing.T, stream sseLines) eventCollector {
	t.Helper()
	collector := eventCollector{idle: make(chan struct{}), done: make(chan []visibilityEvent, 1)}
	go func() {
		var events []visibilityEvent
		completed, idle := false, false
		for line := range stream.lines {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				var event visibilityEvent
				if json.Unmarshal([]byte(data), &event) != nil {
					event.Type = "invalid"
				}
				events = append(events, event)
				completed = completed || event.Type == "agent.session.turn.completed"
				if completed && !idle && event.Type == "agent.session.idle" {
					idle = true
					close(collector.idle)
				}
			}
		}
		collector.done <- events
	}()
	return collector
}

func subagentFixture(kind string, value any) store.ExecutionEvent {
	raw, _ := json.Marshal(value)
	return store.ExecutionEvent{Kind: kind, Payload: raw}
}

// Child work appears only where the official service shows it: Session Turn
// reads and streams carry root work, child Turns are read through the Subagent
// routes with the Session's Agent ID, Subagent lists use the common envelope and
// child Item lists clamp their limit. Tenant B sees none of it.
func TestSubagentVisibilityPublic(t *testing.T) {
	s, _ := store.NewTestStore(t)
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "foreign", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(storeAdmission{s}), api.WithSubagents(s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := pathIDClient{t: t, server: server}
	lease, err := s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close(t.Context()) }()
	writer := lease.Store()
	ctx := t.Context()

	created := openStream(t, server, token, http.MethodPost, "/v1/agents/sessions",
		`{"agent":{"model":"test-model","multi_agent":{"enabled":true,"max_concurrent_subagents":2}},"environment":{"type":"none"},"input":"Delegate one task.","stream":true}`, "subagent-visibility")
	defer created.stop()
	if line := created.next(t); line != ": connected" {
		t.Fatal(line)
	}
	if line := created.next(t); line != "event: agent.session.created" {
		t.Fatal(line)
	}
	var snapshot struct {
		Session struct {
			ID    string `json:"id"`
			Agent struct {
				ID string `json:"id"`
			} `json:"agent"`
		} `json:"session"`
	}
	if data, ok := strings.CutPrefix(created.next(t), "data: "); !ok || json.Unmarshal([]byte(data), &snapshot) != nil || snapshot.Session.Agent.ID == "" {
		t.Fatal("invalid created snapshot", data)
	}
	session, agent := snapshot.Session.ID, snapshot.Session.Agent.ID
	creation := collectEvents(t, created)
	live := openStream(t, server, token, http.MethodGet, "/v1/agents/sessions/"+session+"/events", "", "")
	defer live.stop()
	if line := live.next(t); line != ": connected" {
		t.Fatal(line)
	}
	observed := collectEvents(t, live)

	page, err := s.ListTurns(ctx, tenant, session, "", 10, true)
	if err != nil || len(page.Turns) != 1 {
		t.Fatal(page, err)
	}
	root := page.Turns[0].ID
	host, err := s.CreateDevice(ctx, tenant, "subagent visibility", device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.BindSessionDevice(ctx, tenant, session, host.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.TransitionTurn(ctx, tenant, session, root, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	identity := func(child, parent string, created int64) store.ExecutionEvent {
		return subagentFixture(proto.TypeSubagentIdentity, proto.SubagentIdentityPayload{NativeID: child, ParentNativeID: parent, NativeCreatedAt: created, ParentTurnID: "native-root", SourceItemID: "spawn-" + child})
	}
	opened, finished := int64(1700000001000), int64(1700000002000)
	message := func(child, turn, id string, position int32) store.ExecutionEvent {
		text := "answer " + id
		payload, _ := json.Marshal(proto.OutputMessagePayload{ID: id, Status: "completed", Text: &text})
		return subagentFixture(proto.TypeSubagentItem, proto.SubagentItemPayload{NativeID: child, TurnID: turn, ItemID: id, Position: position, Kind: proto.TypeOutputMessage, Payload: payload})
	}
	facts := []store.ExecutionEvent{
		identity("child", "root", 1700000001), identity("nested", "child", 1700000001),
		subagentFixture(proto.TypeSubagentCoordination, proto.SubagentCoordinationPayload{ID: "spawn", Kind: "create_subagent_call", Status: "completed"}),
		subagentFixture(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: "child", TurnID: "child-turn", Status: store.TurnInProgress, CreatedAtMS: opened, StartedAtMS: &opened}),
		message("child", "child-turn", "first", 0), message("child", "child-turn", "second", 1),
		subagentFixture(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: "child", TurnID: "child-turn", Status: store.TurnCompleted, CreatedAtMS: opened, StartedAtMS: &opened, CompletedAtMS: &finished}),
		subagentFixture(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: "nested", TurnID: "nested-turn", Status: store.TurnCompleted, CreatedAtMS: opened, StartedAtMS: &opened, CompletedAtMS: &finished}),
		subagentFixture(proto.TypeSubagentCoordination, proto.SubagentCoordinationPayload{ID: "wait", Kind: "wait_for_subagents_call", Status: "completed", Recipients: []string{"child"}}),
	}
	if err = writer.AppendTurnEvents(ctx, tenant, session, root, 1, facts); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.TransitionTurn(ctx, tenant, session, root, store.TurnTransition{ExpectedStatus: store.TurnInProgress, Status: store.TurnCompleted}); err != nil {
		t.Fatal(err)
	}

	decode := func(token, path string, status int, value any) string {
		t.Helper()
		code, raw := client.do(token, http.MethodGet, "/v1/agents/sessions/"+session+path, "", nil)
		if code != status || (value != nil && json.Unmarshal([]byte(raw), value) != nil) {
			t.Fatalf("GET %s: %d %s", path, code, raw)
		}
		return raw
	}
	type listPage struct {
		Object  string            `json:"object"`
		FirstID *string           `json:"first_id"`
		LastID  *string           `json:"last_id"`
		HasMore bool              `json:"has_more"`
		Data    []json.RawMessage `json:"data"`
	}
	ids := func(page listPage) []string {
		var result []string
		for _, raw := range page.Data {
			var value struct{ ID string }
			_ = json.Unmarshal(raw, &value)
			result = append(result, value.ID)
		}
		return result
	}
	var subagents listPage
	decode(token, "/subagents?order=asc", 200, &subagents)
	children := ids(subagents)
	if subagents.Object != "list" || len(children) != 2 || subagents.FirstID == nil || *subagents.FirstID != children[0] || subagents.LastID == nil || *subagents.LastID != children[1] || subagents.HasMore {
		t.Fatal("Subagent list envelope", subagents)
	}
	var empty listPage
	decode(token, "/subagents?order=asc&after="+children[1], 200, &empty)
	if empty.Object != "list" || empty.FirstID != nil || empty.LastID != nil || empty.HasMore || len(empty.Data) != 0 {
		t.Fatal("empty Subagent page", empty)
	}
	nativeChild, err := s.GetSubagentIdentity(ctx, tenant, session, "child")
	if err != nil {
		t.Fatal(err)
	}
	child := nativeChild.ID
	type turnBody struct {
		ID         string  `json:"id"`
		AgentID    string  `json:"agent_id"`
		SubagentID *string `json:"subagent_id"`
	}
	var childTurns struct {
		Object string     `json:"object"`
		Data   []turnBody `json:"data"`
	}
	decode(token, "/subagents/"+child+"/turns", 200, &childTurns)
	if childTurns.Object != "list" || len(childTurns.Data) != 1 {
		t.Fatal(childTurns)
	}
	childTurn := childTurns.Data[0].ID
	for _, sub := range children {
		var turns struct{ Data []turnBody }
		decode(token, "/subagents/"+sub+"/turns", 200, &turns)
		for _, turn := range turns.Data {
			// agent_id is the Session's Agent ID for direct and nested children.
			if turn.AgentID != agent || turn.SubagentID == nil || *turn.SubagentID != sub {
				t.Fatal("child Turn identity", sub, turn)
			}
			var retrieved turnBody
			decode(token, "/subagents/"+sub+"/turns/"+turn.ID, 200, &retrieved)
			if retrieved.ID != turn.ID || retrieved.AgentID != agent || retrieved.SubagentID == nil || *retrieved.SubagentID != sub {
				t.Fatal("child Turn retrieval", retrieved)
			}
		}
	}

	// A1: Session Turn reads are root-only, and a child Turn ID is missing there.
	var sessionTurns struct{ Data []turnBody }
	decode(token, "/turns", 200, &sessionTurns)
	if len(sessionTurns.Data) != 1 || sessionTurns.Data[0].ID != root || sessionTurns.Data[0].AgentID != agent || sessionTurns.Data[0].SubagentID != nil {
		t.Fatal("Session Turns include child work", sessionTurns)
	}
	missing := uuid.NewString()
	if decode(token, "/turns/"+childTurn, 404, nil) != decode(token, "/turns/"+missing, 404, nil) {
		t.Fatal("child Turn retrieval differs from a missing Turn")
	}
	if decode(token, "/turns?after="+childTurn, 404, nil) != decode(token, "/turns?after="+missing, 404, nil) {
		t.Fatal("child Turn cursor differs from a missing cursor")
	}
	decode(token, "/turns/"+root, 200, nil)

	// A5: child Item lists clamp; the Subagent and Subagent Turn lists reject.
	for _, path := range []string{"/subagents/" + child + "/items", "/subagents/" + child + "/turns/" + childTurn + "/items"} {
		var all, one, clamped listPage
		decode(token, path+"?order=asc", 200, &all)
		if len(all.Data) != 2 {
			t.Fatal(path, all)
		}
		decode(token, path+"?order=asc&limit=0", 200, &one)
		if one.Object != "list" || len(one.Data) != 1 || !one.HasMore || ids(one)[0] != ids(all)[0] || *one.FirstID != ids(all)[0] {
			t.Fatal(path, "limit=0", one)
		}
		decode(token, path+"?order=asc&limit=101", 200, &clamped)
		if len(clamped.Data) != 2 || clamped.HasMore {
			t.Fatal(path, "limit=101", clamped)
		}
	}
	for _, path := range []string{"/subagents", "/subagents/" + child + "/turns"} {
		for _, limit := range []string{"0", "101"} {
			if raw := decode(token, path+"?limit="+limit, 400, nil); !strings.Contains(raw, "limit must be between 1 and 100") {
				t.Fatal(path, raw)
			}
		}
	}

	// Tenant B cannot read any of these resources.
	for _, path := range []string{"/turns", "/turns/" + root, "/turns/" + childTurn, "/subagents", "/subagents/" + child,
		"/subagents/" + child + "/turns", "/subagents/" + child + "/turns/" + childTurn,
		"/subagents/" + child + "/items?limit=0", "/subagents/" + child + "/turns/" + childTurn + "/items?limit=101"} {
		if raw := decode(foreign, path, 404, nil); strings.Contains(raw, child) || strings.Contains(raw, childTurn) {
			t.Fatal("foreign response exposes child work", path, raw)
		}
	}

	// A2: the creation stream settles on the root Turn's idle as before, and
	// neither stream carries child Turn or child Item events.
	var streamed []visibilityEvent
	select {
	case streamed = <-creation.done:
	case <-time.After(10 * time.Second):
		t.Fatal("creation stream did not settle")
	}
	// The GET stream polls independently; stop it only after it has delivered the
	// root Turn's idle, the last event this test records.
	select {
	case <-observed.idle:
	case <-time.After(10 * time.Second):
		t.Fatal("GET stream did not deliver the root idle")
	}
	live.stop()
	var liveEvents []visibilityEvent
	select {
	case liveEvents = <-observed.done:
	case <-time.After(10 * time.Second):
		t.Fatal("GET stream did not stop")
	}
	for name, events := range map[string][]visibilityEvent{"creation": streamed, "GET": liveEvents} {
		types := map[string]int{}
		for _, event := range events {
			types[event.Type]++
			if (event.TurnID != "" && event.TurnID != root) || (event.Turn != nil && (event.Turn.ID != root || event.Turn.SubagentID != nil)) || (event.Item != nil && event.Item.TurnID != root) {
				t.Fatal(name, "stream carries child work", event)
			}
			if event.Item != nil && event.Item.Type == "create_subagent_call" {
				types["create_subagent_call"]++
			}
		}
		if types["agent.session.subagent.created"] != 2 || types["create_subagent_call"] == 0 || types["agent.session.turn.completed"] != 1 || types["invalid"] != 0 {
			t.Fatal(name, "stream lacks root coordination", types)
		}
		if last := events[len(events)-1]; name == "creation" && last.Type != "agent.session.idle" {
			t.Fatal("creation stream did not end at the settled idle", last.Type)
		}
	}
}
