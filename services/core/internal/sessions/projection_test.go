package sessions

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
)

func TestProjectSource(t *testing.T) {
	t.Run("usage replaces the Turn's usage", func(t *testing.T) {
		var recorded []v1.TokenUsage
		f := &fakeTx{t: t, putTurnUsage: func(usage v1.TokenUsage) error { recorded = append(recorded, usage); return nil }}
		payload := json.RawMessage(`{"usage":{"tokens":{"input_tokens":5,"output_tokens":3,"cached_input_tokens":1,"reasoning_output_tokens":2,"total_tokens":8}}}`)
		if err := ProjectSource(t.Context(), f, Source{Turn: testTurn, Kind: "done", Sequence: 4, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "PutTurnUsage "+testTurn)
		want := v1.TokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8, InputTokensDetails: v1.InputTokenDetails{CachedTokens: 1}, OutputTokensDetails: v1.OutputTokenDetails{ReasoningTokens: 2}}
		if !reflect.DeepEqual(recorded, []v1.TokenUsage{want}) {
			t.Fatalf("usage %+v", recorded)
		}
	})
	t.Run("a new Item is stored and journaled", func(t *testing.T) {
		id := items.Identity(testTurn, "message:m1")
		index := int32(3)
		var changes []SessionChange
		f := &fakeTx{t: t, loadItem: returns(items.Stored{}), putItem: func(items.Change) (*int32, error) { return &index, nil }, appendChanges: collect(&changes)}
		if err := ProjectSource(t.Context(), f, Source{Turn: testTurn, Kind: "output_message", Sequence: 2, Payload: json.RawMessage(`{"id":"m1","status":"completed","text":"hi"}`)}); err != nil {
			t.Fatal(err)
		}
		assertCalls(t, f, "LoadItem "+testTurn+" "+id, "PutItem "+testTurn+" "+id, "AppendChanges agent.session.turn.item.added,agent.session.turn.content_part.added,agent.session.turn.output_text.delta,agent.session.turn.output_text.done,agent.session.turn.content_part.done,agent.session.turn.item.done")
		if changes[0].Event.Item.ID != id || *changes[0].Event.OutputIndex != 3 {
			t.Fatalf("added %+v", changes[0].Event)
		}
	})
}

func TestProjectInput(t *testing.T) {
	f := &fakeTx{t: t, loadInputSource: returns(Source{Turn: testTurn, Kind: "function_call_output", Sequence: 7, Payload: json.RawMessage(`{}`)})}
	if err := ProjectInput(t.Context(), f, 7); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "LoadInputSource 7")
}
