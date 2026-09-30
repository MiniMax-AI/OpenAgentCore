package items

import (
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Stored is what a Session already holds for an Update's Item.
type Stored struct {
	// Item is the stored Item, or the zero Item when the Update adds a new one.
	Item v1.Item
	// NativeMessage reports that the Turn has an assistant message other than
	// this Item. Observe reads it only when the Update NeedsNativeMessage.
	NativeMessage bool
	// FunctionResult is the result the application saved for the call, if any.
	// Observe reads it only for an Update with a ResultCall.
	FunctionResult json.RawMessage
}

// Change is one decided Item write.
type Change struct {
	// Previous is the stored Item, zero for a new one.
	Previous v1.Item
	Item     v1.Item
	// Output reports whether a new Item takes the Turn's next output index.
	// Inputs, function results included, take none.
	Output bool
	// Delta is the text or command output fragment this observation
	// contributed, nil when it contributed none.
	Delta *string
}

// NeedsNativeMessage reports whether Observe reads Stored.NativeMessage for u:
// a legacy aggregate applies only when the Turn recorded no native message.
func (u Update) NeedsNativeMessage() bool {
	return u.LegacyFinal
}

// ResultCall returns the call whose saved result Observe reads for u, a
// function result.
func (u Update) ResultCall() (string, bool) {
	return u.Item.CallID, u.Item.Type == "function_call_output"
}

// Observe decides how an Update projected from an observation of kind changes
// its stored Item. It reports false when the Update is superseded and writes
// nothing.
func Observe(kind string, update Update, stored Stored) (Change, bool, error) {
	if update.LegacyFinal && stored.NativeMessage {
		return Change{}, false, nil
	}
	item, err := Merge(update, stored.Item)
	if err != nil {
		return Change{}, false, err
	}
	if item.Type == "function_call_output" && len(stored.FunctionResult) > 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(stored.FunctionResult, &fields); err != nil {
			return Change{}, false, err
		}
		// Native results may normalize content; public Items retain the saved submission.
		item.Output, item.Error = nil, nil
		if value, ok := fields["output"]; ok {
			item.Output = value
		}
		if value, ok := fields["error"]; ok {
			item.Error = value
		}
	}
	change := Change{Previous: stored.Item, Item: item, Output: kind != "message" && item.Type != "function_call_output"}
	switch kind {
	case proto.TypeDelta:
		change.Delta = update.Item.Content[0].Text
	case proto.TypeCommandOutput:
		change.Delta = update.CommandOutputDelta
	}
	return change, true, nil
}
