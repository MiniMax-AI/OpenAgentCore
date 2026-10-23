package codex

import (
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// ItemBuffers holds per-itemId state for streamed notifications.
// agentMessage deltas drive incremental TypeDelta frames while the completed
// item anchors the final text. A reasoning item streams one of its two native
// channels, recorded here from its first delta.
type ItemBuffers struct {
	Reasoning map[string]reasoningStream
	AgentText map[string]string
}

// reasoningStream is the native channel a reasoning item streams, the last
// native part index seen on it and the reported part it maps to.
type reasoningStream struct {
	channel      string
	native, part int
}

// NewItemBuffers returns an empty buffer set.
func NewItemBuffers() *ItemBuffers {
	return &ItemBuffers{
		Reasoning: map[string]reasoningStream{},
		AgentText: map[string]string{},
	}
}

// DispatchStartedItem maps the item.started variants we care about
// (tool_call surfaces) into proto envelopes. The function never returns
// terminal envelopes; per Codex, only item/completed + turn/completed
// can finish a turn.
//
// item types we silence by intent:
//
//   - reasoning / agentMessage / userMessage: text is streamed via deltas
//     and re-stamped in item/completed; reasoning is reported by the Session
//   - error: surfaced by turn/completed.status="failed" instead
func DispatchStartedItem(runID string, item ThreadItem) ([]proto.Envelope, error) {
	switch item.Type {
	case "commandExecution":
		return wrapToolCall(runID, item.ID, "Bash", map[string]any{
			"command": item.Command,
			"cwd":     item.Cwd,
		})
	case "fileChange":
		return wrapToolCall(runID, item.ID, "Edit", map[string]any{
			"changes": item.Changes,
		})
	case "mcpToolCall":
		name := fmt.Sprintf("mcp__%s__%s", item.Server, item.Tool)
		return wrapToolCall(runID, item.ID, name, argMap(item.Arguments))
	case "dynamicToolCall":
		// dynamicToolCall has no server; tool name is namespaced via
		// item.Namespace when present.
		name := item.Tool
		if item.Namespace != "" {
			name = item.Namespace + "::" + item.Tool
		}
		return wrapToolCall(runID, item.ID, name, argMap(item.Arguments))
	case "webSearch":
		return wrapToolCall(runID, item.ID, "WebSearch", map[string]any{
			"query": item.Query,
		})
	}
	return nil, nil
}

// DispatchCompletedItem folds an item.completed payload into envelopes.
// An agentMessage returns its text so the session can stamp it into
// DonePayload.Content; tool-call variants produce an "after" envelope so the
// UI gets a stage transition.
//
// Returns the agent text body for agentMessage items so the session can
// stamp it into DonePayload.Content. Empty string for everything else.
func DispatchCompletedItem(runID string, item ThreadItem, bufs *ItemBuffers) (envelopes []proto.Envelope, agentText string, err error) {
	switch item.Type {
	case "agentMessage":
		body := bufs.AgentText[item.ID]
		delete(bufs.AgentText, item.ID)
		if body == "" {
			body = item.Text
		}
		return nil, body, nil
	case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall", "webSearch":
		// Surface a tool_call "after" so the UI flips status.
		name := toolNameForItem(item)
		result := map[string]any{}
		if item.ExitCode != nil {
			result["exit_code"] = *item.ExitCode
		}
		if item.Status != "" {
			result["status"] = item.Status
		}
		envs, err := wrapToolCallStage(runID, item.ID, name, nil, result, "after")
		if err != nil {
			return nil, "", err
		}
		return envs, "", nil
	}
	return nil, "", nil
}

// FoldDeltaIntoBuffer accumulates an agentMessage delta under its itemId and
// returns the running text.
func FoldDeltaIntoBuffer(bufs *ItemBuffers, itemID, delta string) string {
	if delta == "" || itemID == "" {
		return ""
	}
	bufs.AgentText[itemID] += delta
	return bufs.AgentText[itemID]
}

func toolNameForItem(item ThreadItem) string {
	switch item.Type {
	case "commandExecution":
		return "Bash"
	case "fileChange":
		return "Edit"
	case "mcpToolCall":
		return fmt.Sprintf("mcp__%s__%s", item.Server, item.Tool)
	case "dynamicToolCall":
		if item.Namespace != "" {
			return item.Namespace + "::" + item.Tool
		}
		return item.Tool
	case "webSearch":
		return "WebSearch"
	}
	return item.Type
}

func wrapToolCall(runID, id, name string, args map[string]any) ([]proto.Envelope, error) {
	return wrapToolCallStage(runID, id, name, args, nil, "before")
}

func wrapToolCallStage(runID, id, name string, args, result map[string]any, stage string) ([]proto.Envelope, error) {
	env, err := proto.NewEnvelope(proto.TypeToolCall, runID, proto.ToolCallPayload{
		ID:     id,
		Name:   name,
		Stage:  stage,
		Args:   args,
		Result: result,
	})
	if err != nil {
		return nil, fmt.Errorf("codex: tool_call envelope: %w", err)
	}
	return []proto.Envelope{env}, nil
}

// argMap normalises ThreadItem.Arguments — which arrives as
// any (typically map[string]any decoded from JSON) — into a
// map suitable for ToolCallPayload.Args. Non-object args are wrapped
// under "value" to avoid silently dropping arrays / strings.
func argMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	// Round-trip through JSON for any unexpected shape so the wire
	// payload stays structured. Falls back to {"value": <json>} when
	// it's not an object.
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"value": fmt.Sprintf("%v", v)}
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		return obj
	}
	return map[string]any{"value": json.RawMessage(raw)}
}

// nativeReasoningParts is what Codex shows for a completed reasoning item: its
// summary when it has one, otherwise its raw reasoning text.
func nativeReasoningParts(item ThreadItem) []string {
	if len(item.Summary) > 0 {
		return item.Summary
	}
	return item.Content
}
