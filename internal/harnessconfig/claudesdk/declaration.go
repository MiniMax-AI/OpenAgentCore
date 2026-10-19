// Package claudesdk declares the Claude Code Harness.
package claudesdk

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration is the only authored description of the Claude Code Harness. The
// Runtime may narrow Capabilities to the installed bundle, never widen them.
// Whitespace-only text is unsupported because the bridge and Anthropic-compatible
// providers reject text without non-whitespace characters.
var Declaration = harnessconfig.Declaration{
	Kind:  "claude_sdk",
	Label: "Claude Code",
	Configuration: harnessconfig.Configuration{
		ValidateNativeConfig: validateNativeConfig,
		Providers:            []harnessconfig.Provider{{Protocol: "anthropic"}},
	},
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:     proto.CapabilitySupported,
		Streaming:                proto.CapabilitySupported,
		Usage:                    proto.CapabilitySupported,
		Resume:                   proto.CapabilitySupported,
		RetainedNativeHistory:    proto.CapabilitySupported,
		NativeSessionRecovery:    proto.CapabilitySupported,
		Steering:                 proto.CapabilitySupported,
		MessageItems:             proto.CapabilitySupported,
		ToolObservations:         proto.CapabilitySupported,
		EnvironmentNone:          proto.CapabilitySupported,
		LocalEnvironment:         proto.CapabilitySupported,
		Preparation:              proto.CapabilitySupported,
		WorkspaceReadPreparation: proto.CapabilitySupported,
		WorkspaceOutputExport:    proto.CapabilitySupported,
		ExecutionControls:        proto.CapabilitySupported,
		StructuredOutput:         proto.CapabilitySupported,
		ToolSearch:               proto.CapabilitySupported,
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
	// The SDK takes effort low to max. It has no verbosity, summary or service
	// tier, and enabling programmatic tool calling is not wired. Native WebSearch
	// and WebFetch are live and have no cached mode or settings: disabled removes
	// them, while live and an absent control keep them. Medium is the official
	// default verbosity and needs no setting.
	Execution: harnessconfig.ExecutionOptions{
		WebSearchModes:          []string{"disabled", "live"},
		ReasoningEfforts:        []string{"low", "medium", "high", "xhigh", "max"},
		ProgrammaticToolCalling: []bool{false},
		TextVerbosities:         []string{"medium"},
	},
	Placements:             []string{"none", "openai_hosted", "self_hosted"},
	MCPOrigins:             []string{"service", "environment"},
	WhitespaceOnlyText:     proto.CapabilityUnsupported,
	ValidateConfiguration:  validateConfiguration,
	ValidateTools:          validateTools,
	ValidateMessageInput:   validateMessageInput,
	ValidateFunctionResult: validateFunctionResult,
}

var (
	mcpLabel = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	mcpTool  = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
)

// validateConfiguration holds the packaged adapter's execution limits on a saved
// Agent and Environment, not saved-Agent schema rules.
func validateConfiguration(admission harnessconfig.Admission) error {
	environment := admission.Environment
	if environment == nil || (environment.Type != "none" && environment.Type != "openai_hosted" && environment.Type != "self_hosted") || strings.TrimSpace(admission.Model) == "" {
		return harnessconfig.ErrInvalidInput
	}
	if admission.TextFormat == "json_schema" {
		if err := proto.ValidateBinary64Schema(admission.TextSchema); err != nil {
			return err
		}
		if admission.MultiAgent {
			return errors.New("Structured output requires a single Agent.")
		}
		if environment.Extensions {
			return errors.New("Structured output with environment Skills or Plugins is not supported.")
		}
		for _, raw := range admission.Tools {
			var tool struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &tool) != nil || (tool.Type != "function" && tool.Type != "web_search" && tool.Type != "programmatic_tool_calling") {
				return harnessconfig.ErrInvalidInput
			}
		}
	} else if admission.TextFormat != "" && admission.TextFormat != "text" {
		return harnessconfig.ErrInvalidInput
	}
	search, otherTools := false, false
	for _, raw := range admission.Tools {
		var tool struct {
			Type string `json:"type"`
			Mode string `json:"mode"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			return harnessconfig.ErrInvalidInput
		}
		// environment:none runs no native tools, so live search has no home there.
		if tool.Type == "web_search" && tool.Mode == "live" && environment.Type == "none" {
			return errors.New("Live web search requires a managed or self-hosted execution Environment.")
		}
		search = search || tool.Type == "tool_search"
		otherTools = otherTools || (tool.Type != "function" && tool.Type != "tool_search" && tool.Type != "web_search" && tool.Type != "programmatic_tool_calling")
	}
	if search && (admission.MultiAgent || otherTools || environment.Extensions || admission.TextFormat == "json_schema") {
		return errors.New("Tool discovery requires a single-agent function profile without Skills or Plugins.")
	}
	return harnessconfig.RejectSubagentTools(admission, "function", "mcp")
}

// validateTools applies execution limits of the packaged adapter. Shared
// resolution still owns URL, transport and credential-binding validation.
func validateTools(tools []proto.FunctionTool, servers []proto.MCPHTTPServer) error {
	for _, server := range servers {
		if !mcpLabel.MatchString(server.ServerLabel) || server.ServerLabel == "functions" || strings.Contains(server.ServerURL, "#") {
			return errors.New("The configured engine does not support this HTTP MCP declaration.")
		}
		if server.AllowedTools != nil {
			for _, name := range *server.AllowedTools {
				if !mcpTool.MatchString(name) {
					return errors.New("The configured engine does not support this MCP tool name.")
				}
			}
		}
	}
	for _, tool := range tools {
		var schema struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(tool.Parameters, &schema) != nil || schema.Type != "object" {
			return errors.New("The configured engine currently requires function schemas with root type object.")
		}
	}
	return nil
}

func validateFunctionResult(result proto.FunctionResultPayload) error {
	for _, part := range result.Content {
		if part.Type == "input_image" && !result.Success {
			return harnessconfig.ErrInvalidInput
		}
	}
	if !nativeImages(result.Content, false) {
		return harnessconfig.ErrInvalidInput
	}
	return nil
}

func validateMessageInput(input proto.MessageInput) error {
	for _, message := range input {
		if !nativeImages(message.Content, true) {
			return harnessconfig.ErrInvalidInput
		}
	}
	return nil
}

var (
	remoteImage = regexp.MustCompile(`^https?://\S+$`)
	inlineImage = regexp.MustCompile(`^data:image/(?:png|jpeg|gif|webp);base64,([A-Za-z0-9+/]+={0,2})$`)
)

// nativeImages reports whether every image maps to a native block: a user
// message takes a remote URL or inline base64, an MCP tool result only inline
// base64, in the media types the pinned SDK declares.
func nativeImages(content []proto.InputContent, remote bool) bool {
	for _, part := range content {
		if part.Type != "input_image" || part.ImageURL == nil {
			continue
		}
		if remote && remoteImage.MatchString(*part.ImageURL) {
			continue
		}
		match := inlineImage.FindStringSubmatch(*part.ImageURL)
		if match == nil {
			return false
		}
		if data, err := base64.StdEncoding.DecodeString(match[1]); err != nil || base64.StdEncoding.EncodeToString(data) != match[1] {
			return false
		}
	}
	return true
}

func validateNativeConfig(config proto.HarnessConfig) bool {
	for key, value := range config {
		switch key {
		case "thinking":
			thinking, ok := value.(map[string]any)
			if !ok || !validThinking(thinking) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// The pinned SDK's native ThinkingConfig union; deprecated maxThinkingTokens
// is intentionally absent so a thinking budget has exactly one owner.
func validThinking(thinking map[string]any) bool {
	kind, ok := thinking["type"].(string)
	if !ok || (kind != "adaptive" && kind != "enabled" && kind != "disabled") {
		return false
	}
	for key, value := range thinking {
		switch key {
		case "type":
		case "budgetTokens":
			n, ok := value.(float64)
			if kind != "enabled" || !ok || n < 1 || n > 9007199254740991 || math.Trunc(n) != n {
				return false
			}
		case "display":
			text, ok := value.(string)
			if kind == "disabled" || !ok || (text != "summarized" && text != "omitted") {
				return false
			}
		default:
			return false
		}
	}
	return true
}
