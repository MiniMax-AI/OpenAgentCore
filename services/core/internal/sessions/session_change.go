package sessions

import (
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
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
