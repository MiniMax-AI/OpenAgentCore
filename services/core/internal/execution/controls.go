package execution

import (
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// executionControls assembles the official execution options for the Runtime.
// It carries a value only when the Session sets it or the official API defines
// a default (text verbosity medium, web_search context_size medium); everything
// else is absent and follows the Harness's native default. Auto service tier
// means unset.
func executionControls(agent v1.Agent, tools executionToolSet) *proto.ExecutionControls {
	controls := &proto.ExecutionControls{WebSearch: tools.WebSearch, ProgrammaticToolCalling: tools.ProgrammaticToolCalling, TextVerbosity: agent.Text.Verbosity}
	if controls.TextVerbosity == "" {
		controls.TextVerbosity = "medium"
	}
	if agent.Reasoning.Effort != nil {
		controls.ReasoningEffort = *agent.Reasoning.Effort
	}
	if agent.Reasoning.Summary != nil {
		controls.ReasoningSummary = *agent.Reasoning.Summary
	}
	if agent.ServiceTier != "auto" {
		controls.ServiceTier = agent.ServiceTier
	}
	if agent.Text.Format.Type == "json_schema" {
		controls.OutputFormat = &proto.OutputFormat{Type: "json_schema", Schema: agent.Text.Format.Schema}
	}
	return controls
}
