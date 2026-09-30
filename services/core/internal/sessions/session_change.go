package sessions

import (
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
)

// A Session keeps at most RetainedChanges public changes and
// RetainedChangeBytes of their payloads for live readers after each
// transaction, and always keeps its newest change even when it is larger.
const (
	RetainedChanges     = 256
	RetainedChangeBytes = 64 << 20
)

// SessionChange keeps transition snapshots separate from public response rendering.
type SessionChange struct {
	Sequence                 int64                     `json:"-"`
	Event                    v1.SessionEvent           `json:"event"`
	Turn                     *Turn                     `json:"turn,omitempty"`
	SessionUsage             json.RawMessage           `json:"session_usage,omitempty"`
	RequiredActions          []v1.FunctionCallAction   `json:"required_actions,omitempty"`
	EnvironmentInputActivity *EnvironmentInputActivity `json:"environment_input_activity,omitempty"`
	// EnvironmentFailure is set on the agent.session.failed snapshot of a hosted
	// provisioning failure, which also ends live event streams.
	EnvironmentFailure *EnvironmentFailure `json:"environment_failure,omitempty"`
	// Settled marks an idle or failed snapshot recorded when a Turn ends, or when
	// the latest input reservation stops being pending (expired, cancelled or
	// failed). A reservation made while the ending Turn captured Artifacts can
	// still be pending and start a later Turn. It is internal, never a wire
	// field; snapshots recorded without it read as unsettled.
	Settled bool `json:"settled,omitempty"`
}

// ItemChanges returns the public changes that report an Item change, in
// publication order. outputIndex is the Item's output index, nil when it has
// none.
func ItemChanges(change items.Change, outputIndex *int32) []SessionChange {
	events := items.Events(change, outputIndex)
	changes := make([]SessionChange, len(events))
	for i, event := range events {
		changes[i] = SessionChange{Event: event}
	}
	return changes
}
