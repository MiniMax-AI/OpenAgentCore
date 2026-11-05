package execution

import (
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
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
	if agent.Reasoning.Summary != nil {
		controls.ReasoningSummary = *agent.Reasoning.Summary
	}
	if agent.Text.Format.Type == "json_schema" {
		controls.OutputFormat = &proto.OutputFormat{Type: "json_schema", Schema: agent.Text.Format.Schema}
	}
	return controls
}

// turnSettings assembles the Runtime's settings from the settings a root Turn
// bound. Auto service tier and review none mean unset.
func turnSettings(bound sessions.TurnSettings) proto.TurnSettings {
	turn := proto.TurnSettings{Model: bound.Model}
	if bound.ReasoningEffort != nil {
		turn.ReasoningEffort = *bound.ReasoningEffort
	}
	if bound.ServiceTier != "auto" {
		turn.ServiceTier = bound.ServiceTier
	}
	if bound.Review == "auto_review" {
		turn.Review = bound.Review
	}
	return turn
}
