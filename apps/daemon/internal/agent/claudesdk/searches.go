package claudesdk

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type searchState struct {
	calls map[string]proto.ToolObservation
}

func (c *searchState) receive(event bridgeEvent, start startRequest, sessionID string, emit func(string, any)) error {
	n := event.Observation
	if start.Workspace == nil && start.WebSearch != "live" || sessionID == "" || event.SessionID != sessionID || event.ID == "" || n == nil ||
		n.Kind != "web_search" || n.Action == nil || (n.Action.Type != "search" && n.Action.Type != "open_page") ||
		(n.Action.Type == "search") != (n.Action.Query != nil) || (n.Action.Type == "open_page") != (n.Action.URL != nil) ||
		n.Name != "" || n.Command != "" || n.Cwd != nil || n.ExitCode != nil || n.DurationMS != nil || n.Server != "" ||
		len(n.Arguments) != 0 || len(n.Output) != 0 || len(n.Error) != 0 || n.Content != nil {
		return fmt.Errorf("claudesdk: invalid web search observation")
	}
	previous, exists := c.calls[event.ID]
	if (event.Stage == "before" && (exists || n.Status != "in_progress")) ||
		(event.Stage == "after" && (!exists || previous.Status != "in_progress" ||
			(n.Status != "completed" && n.Status != "failed" && n.Status != "incomplete"))) ||
		(event.Stage != "before" && event.Stage != "after") {
		return fmt.Errorf("claudesdk: inconsistent web search observation")
	}
	c.calls[event.ID] = *n
	emit(proto.TypeToolCall, proto.ToolCallPayload{ID: event.ID, Name: "web_search", Stage: event.Stage, Observation: n})
	return nil
}

func (c *searchState) complete() bool {
	for _, call := range c.calls {
		if call.Status == "in_progress" {
			return false
		}
	}
	return true
}

func (c *searchState) close(emit func(string, any)) {
	for id, call := range c.calls {
		if call.Status != "in_progress" {
			continue
		}
		call.Status = "incomplete"
		c.calls[id] = call
		emit(proto.TypeToolCall, proto.ToolCallPayload{ID: id, Name: "web_search", Stage: "after", Observation: &call})
	}
}
