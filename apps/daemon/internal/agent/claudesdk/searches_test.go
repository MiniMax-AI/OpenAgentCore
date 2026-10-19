package claudesdk

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func searchEvent(id, stage, status string, action *proto.ToolWebSearchAction) bridgeEvent {
	return bridgeEvent{Type: "search_observation", SessionID: "native-session", ID: id, Stage: stage,
		Observation: &proto.ToolObservation{Kind: "web_search", Status: status, Action: action}}
}

func TestSearchObservationLifecycle(t *testing.T) {
	state := searchState{calls: map[string]proto.ToolObservation{}}
	start := startRequest{Workspace: &workspaceProfile{}}
	var events []proto.ToolCallPayload
	emit := func(kind string, payload any) { events = append(events, payload.(proto.ToolCallPayload)) }
	query, url := "agents api", "https://example.com"
	search := &proto.ToolWebSearchAction{Type: "search", Query: &query}
	if err := state.receive(searchEvent("one", "before", "in_progress", search), start, "native-session", emit); err != nil || state.complete() {
		t.Fatal("search was not pending", err)
	}
	if err := state.receive(searchEvent("one", "before", "in_progress", search), start, "native-session", emit); err == nil {
		t.Fatal("duplicate search accepted")
	}
	if err := state.receive(searchEvent("one", "after", "completed", search), start, "native-session", emit); err != nil || !state.complete() {
		t.Fatal("search did not complete", err)
	}
	open := &proto.ToolWebSearchAction{Type: "open_page", URL: &url}
	if err := state.receive(searchEvent("two", "before", "in_progress", open), start, "native-session", emit); err != nil {
		t.Fatal(err)
	}
	state.close(emit)
	if state.calls["two"].Status != "incomplete" || len(events) != 4 || events[3].Observation.Action.URL == nil {
		t.Fatal("unfinished page open was not closed", events)
	}
	for name, event := range map[string]bridgeEvent{
		"action":  searchEvent("bad", "before", "in_progress", &proto.ToolWebSearchAction{Type: "find_in_page", Pattern: &query}),
		"missing": searchEvent("bad", "before", "in_progress", &proto.ToolWebSearchAction{Type: "search"}),
		"session": {Type: "search_observation", SessionID: "other", ID: "bad", Stage: "before", Observation: &proto.ToolObservation{Kind: "web_search", Status: "in_progress", Action: search}},
	} {
		if err := state.receive(event, start, "native-session", emit); err == nil {
			t.Fatal("invalid observation accepted", name)
		}
	}
}

func TestSearchObservationOnEnvironmentNoneNeedsLiveSearch(t *testing.T) {
	query := "agents api"
	event := searchEvent("one", "before", "in_progress", &proto.ToolWebSearchAction{Type: "search", Query: &query})
	emit := func(string, any) {}
	for _, mode := range []string{"", "live"} {
		state := searchState{calls: map[string]proto.ToolObservation{}}
		if err := state.receive(event, startRequest{WebSearch: mode}, "native-session", emit); (err == nil) != (mode == "live") {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
}
