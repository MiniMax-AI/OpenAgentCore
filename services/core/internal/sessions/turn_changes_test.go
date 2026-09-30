package sessions

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

const (
	testSession = "8c6f1fd1-7f7d-4bd8-9a55-5b0f1c1f4d31"
	testTurn    = "bff31a40-9a63-4a49-aebe-89dfe9dd5268"
)

func turnWith(status string) Turn {
	return Turn{ID: testTurn, SessionID: testSession, Status: status, CompletedAt: time.Unix(1700000000, 0), Outcome: json.RawMessage(`{"private":true}`)}
}

func types(changes []SessionChange) []string {
	var kinds []string
	for _, change := range changes {
		kinds = append(kinds, change.Event.Type)
	}
	return kinds
}

func TestTerminalStatus(t *testing.T) {
	for status, want := range map[string]bool{
		TurnQueued: false, TurnInProgress: false, TurnWaiting: false,
		TurnCompleted: true, TurnFailed: true, TurnCancelled: true, "": false,
	} {
		if TerminalStatus(status) != want {
			t.Errorf("%q: want %v", status, want)
		}
	}
}

func TestTurnChangesReportTheNewStatus(t *testing.T) {
	for _, test := range []struct {
		status  string
		created bool
		want    []string
	}{
		{TurnQueued, true, []string{"agent.session.turn.created"}},
		{TurnInProgress, false, []string{"agent.session.turn.in_progress"}},
		{TurnWaiting, false, nil},
		{TurnCancelled, false, []string{"agent.session.turn.cancelled"}},
	} {
		turn := turnWith(test.status)
		changes := TurnChanges(turn, test.created)
		if !reflect.DeepEqual(types(changes), test.want) {
			t.Fatalf("%s: %q", test.status, types(changes))
		}
		for _, change := range changes {
			if change.Turn.Outcome != nil || change.Event.TurnID != testTurn || change.Turn.Status != test.status {
				t.Fatalf("%s: snapshot %+v", test.status, change)
			}
		}
		if turn.Outcome == nil {
			t.Fatal("the caller's Turn lost its outcome")
		}
	}
}

func TestActivityChangeDerivesSessionActivity(t *testing.T) {
	usage := json.RawMessage(`{"total_tokens":3}`)
	actions := []v1.FunctionCallAction{{Type: "function_call", CallID: "call"}}
	for _, test := range []struct {
		status  string
		actions []v1.FunctionCallAction
		want    string
		settled bool
	}{
		{TurnQueued, nil, "agent.session.in_progress", false},
		{TurnInProgress, nil, "agent.session.in_progress", false},
		{TurnWaiting, actions, "agent.session.requires_action", false},
		{TurnWaiting, nil, "agent.session.in_progress", false},
		{TurnCompleted, nil, "agent.session.idle", true},
		{TurnCancelled, nil, "agent.session.idle", true},
		{TurnFailed, nil, "agent.session.failed", true},
	} {
		change := ActivityChange(turnWith(test.status), usage, test.actions)
		if change.Event.Type != test.want || change.Settled != test.settled || change.Turn.Outcome != nil ||
			string(change.SessionUsage) != string(usage) || !reflect.DeepEqual(change.RequiredActions, test.actions) {
			t.Fatalf("%s: %+v", test.status, change)
		}
	}
}

func TestEndTurnSettlesArtifactsItemsAndActivity(t *testing.T) {
	text := func(value string) *string { return &value }
	zero, index := int32(0), int32(1)
	second := UnfinishedItem{Position: 9, Item: v1.Item{ID: "second", TurnID: testTurn, Type: "web_search_call", Status: "in_progress"}, OutputIndex: &index}
	first := UnfinishedItem{Position: 4, OutputIndex: &zero, Item: v1.Item{ID: "first", TurnID: testTurn, Type: "message", Role: "assistant", Status: "in_progress", Content: []v1.ItemContent{{Type: "output_text", Text: text("partial")}}}}
	usage := json.RawMessage(`{"total_tokens":3}`)
	for _, test := range []struct {
		status    string
		artifacts ArtifactSettlement
		activity  string
	}{
		{TurnCompleted, PublishArtifacts, "agent.session.idle"},
		{TurnFailed, DiscardArtifacts, "agent.session.failed"},
		{TurnCancelled, DiscardArtifacts, "agent.session.idle"},
	} {
		turn := turnWith(test.status)
		end := EndTurn(turn, Ending{Unfinished: []UnfinishedItem{second, first}, Usage: usage})
		if !end.TerminalActivity || end.Artifacts != test.artifacts || (test.artifacts == PublishArtifacts) != end.PublishedAt.Equal(turn.CompletedAt) {
			t.Fatalf("%s: settlement %+v", test.status, end)
		}
		if !reflect.DeepEqual(end.FinishedItems, []string{"first", "second"}) || end.FinishedStatus != "incomplete" {
			t.Fatalf("%s: items %q %q", test.status, end.FinishedItems, end.FinishedStatus)
		}
		want := []string{
			"agent.session.turn.output_text.done", "agent.session.turn.content_part.done", "agent.session.turn.item.done",
			"agent.session.turn.item.done",
			"agent.session.turn." + test.status,
			test.activity,
		}
		if !reflect.DeepEqual(types(end.Changes), want) {
			t.Fatalf("%s: changes %q", test.status, types(end.Changes))
		}
		if end.Changes[2].Event.Item.ID != "first" || end.Changes[2].Event.Item.Status != "incomplete" || *end.Changes[2].Event.Item.Content[0].Text != "partial" ||
			end.Changes[3].Event.Item.ID != "second" || *end.Changes[3].Event.OutputIndex != 1 {
			t.Fatalf("%s: finished Items %+v %+v", test.status, end.Changes[2].Event, end.Changes[3].Event)
		}
		activity := end.Changes[len(end.Changes)-1]
		if !activity.Settled || string(activity.SessionUsage) != string(usage) {
			t.Fatalf("%s: activity %+v", test.status, activity)
		}
	}
}
