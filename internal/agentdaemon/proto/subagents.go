package proto

import "encoding/json"

// TypeSubagentIdentity carries verified native identity facts; lifecycle uses a separate effect.
const TypeSubagentIdentity = "subagent_identity"

// SubagentIdentityPayload is scoped by the authenticated Run envelope. The service
// supplies its Session, project, device and public identity; none comes from here.
type SubagentIdentityPayload struct {
	NativeID        string  `json:"native_id"`
	ParentNativeID  string  `json:"parent_native_id"`
	NativeCreatedAt int64   `json:"native_created_at"`
	ParentTurnID    string  `json:"parent_turn_id"`
	SourceItemID    string  `json:"source_item_id"`
	Name            *string `json:"name"`
	Instructions    *string `json:"instructions"`
}

const (
	TypeSubagentLifecycle = "subagent_lifecycle"
	TypeSubagentTurn      = "subagent_turn"
	TypeSubagentItem      = "subagent_item"
)

// SubagentLifecyclePayload reports a successful native effect, never a task's
// completion or a process disconnect. EffectID is stable across history reads.
// Active is emitted only for an actual reopen; an already-active resume is a no-op.
type SubagentLifecyclePayload struct {
	NativeID     string `json:"native_id"`
	EffectID     string `json:"effect_id"`
	Status       string `json:"status"`
	OccurredAtMS int64  `json:"occurred_at_ms"`
}

// SubagentTurnPayload describes child-owned work. Timestamps are native facts or
// an immutable receipt of confirmed cancellation when native history omits it.
// Converting seconds to milliseconds does not establish finer precision.
// Emit identity first, then an active Turn, its Items, and its terminal snapshot.
// Unknown token measurements stay nil. Core owns all public resource IDs.
type SubagentTurnPayload struct {
	NativeID      string        `json:"native_id"`
	TurnID        string        `json:"turn_id"`
	Status        string        `json:"status"`
	CreatedAtMS   int64         `json:"created_at_ms"`
	StartedAtMS   *int64        `json:"started_at_ms"`
	CompletedAtMS *int64        `json:"completed_at_ms"`
	Usage         *UsagePayload `json:"usage"`
}

// SubagentItemPayload reuses ordinary message/tool observations. Payload is a
// complete snapshot, not a delta. Position is the native order within its Turn.
// NativeItem and engine-specific payloads are not admitted by this contract.
type SubagentItemPayload struct {
	NativeID string          `json:"native_id"`
	TurnID   string          `json:"turn_id"`
	ItemID   string          `json:"item_id"`
	Position int32           `json:"position"`
	Kind     string          `json:"kind"`
	Payload  json.RawMessage `json:"payload"`
}

const TypeSubagentCoordination = "subagent_coordination"

// SubagentCoordinationPayload reports the native coordination operation in the
// common vocabulary. Actor/recipients are native identities, resolved by Core.
// An empty ActorID denotes the authenticated root, never an arbitrary Agent.
// A child-owned operation is carried as a SubagentItem with this payload.
type SubagentCoordinationPayload struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Status          string   `json:"status"`
	ActorID         string   `json:"actor_id"`
	Recipients      []string `json:"recipients"`
	Text            *string  `json:"text"`
	Model           *string  `json:"model"`
	ReasoningEffort *string  `json:"reasoning_effort"`
}
