// Package codex declares the Codex Harness.
package codex

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration is the only authored description of the Codex Harness. The
// Runtime may narrow Capabilities for the installed host, never widen them.
var Declaration = harnessconfig.Declaration{
	Kind:  "codex",
	Label: "Codex",
	Configuration: harnessconfig.Configuration{
		// context_window maps to model_context_window; Codex has no native
		// max_output_tokens setting.
		Providers: []harnessconfig.Provider{{Protocol: "responses"}},
	},
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:     proto.CapabilitySupported,
		Streaming:                proto.CapabilitySupported,
		Usage:                    proto.CapabilitySupported,
		Resume:                   proto.CapabilitySupported,
		RetainedNativeHistory:    proto.CapabilitySupported,
		NativeSessionRecovery:    proto.CapabilitySupported,
		MessageItems:             proto.CapabilitySupported,
		ToolObservations:         proto.CapabilitySupported,
		EnvironmentNone:          proto.CapabilitySupported,
		LocalEnvironment:         proto.CapabilitySupported,
		Preparation:              proto.CapabilitySupported,
		WorkspaceReadPreparation: proto.CapabilitySupported,
		WorkspaceOutputExport:    proto.CapabilitySupported,
		ExecutionControls:        proto.CapabilitySupported,
		StructuredOutput:         proto.CapabilitySupported,
		ToolSearch:               proto.CapabilityUnsupported,
		MessageImages:            proto.CapabilitySupported,
		FunctionResultImages:     proto.CapabilitySupported,
		SubagentControl:          proto.CapabilitySupported,
		DurableInputReceipts:     proto.CapabilitySupported,
		DurableTurns:             proto.CapabilitySupported,
		FunctionTools:            proto.CapabilitySupported,
		MCPHTTPTools:             proto.CapabilitySupported,
		MCPHTTPRequired:          proto.CapabilitySupported,
		MCPHTTPBearerAuth:        proto.CapabilitySupported,
	},
	// Codex maps every official value natively: web_search, tools.web_search.*,
	// model_reasoning_effort, model_reasoning_summary, service_tier,
	// model_verbosity and features.code_mode. Native indexed search has no
	// official value. Official auto service tier means unset, and fast is Codex's
	// native alias of priority. Codex ignores a value the selected model does not
	// support.
	Execution: harnessconfig.ExecutionOptions{
		WebSearchModes:          []string{"disabled", "cached", "live"},
		WebSearchSettings:       true,
		ReasoningEfforts:        []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
		ReasoningSummaries:      []string{"concise", "detailed", "auto"},
		ServiceTiers:            []string{"default", "flex", "priority", "fast"},
		ProgrammaticToolCalling: []bool{true, false},
		TextVerbosities:         []string{"low", "medium", "high"},
	},
	Placements:           []string{"none", "self_hosted", "openai_hosted"},
	MCPOrigins:           []string{"service", "environment"},
	WhitespaceOnlyText:   proto.CapabilitySupported,
	ValidateMessageInput: rejectRemoteImages,
	ValidateFunctionResult: func(result proto.FunctionResultPayload) error {
		return rejectRemoteImages(proto.MessageInput{{Content: result.Content}})
	},
	ValidateConfiguration: func(admission harnessconfig.Admission) error {
		// Subagent observation turns native code_mode off.
		if admission.MultiAgent && enablesProgrammaticToolCalling(admission.Tools) {
			return errors.New("programmatic_tool_calling.enabled=true is not supported with multi_agent by this Harness.")
		}
		return nil
	},
}

// Native Codex rejects http and https image URLs in turns and tool results.
func rejectRemoteImages(input proto.MessageInput) error {
	for _, message := range input {
		for _, part := range message.Content {
			if part.ImageURL == nil {
				continue
			}
			if scheme, _, ok := strings.Cut(*part.ImageURL, ":"); ok && (strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")) {
				return harnessconfig.ErrInvalidInput
			}
		}
	}
	return nil
}

func enablesProgrammaticToolCalling(tools []json.RawMessage) bool {
	for _, raw := range tools {
		var tool struct {
			Type    string `json:"type"`
			Enabled *bool  `json:"enabled"`
		}
		if json.Unmarshal(raw, &tool) == nil && tool.Type == "programmatic_tool_calling" && tool.Enabled != nil && *tool.Enabled {
			return true
		}
	}
	return false
}
