// Package claudesdk declares the Claude Code Harness.
package claudesdk

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration is the only authored description of the Claude Code Harness. The
// Runtime may narrow Capabilities to the installed bundle, never widen them.
var Declaration = harnessconfig.Declaration{
	Kind:  "claude_sdk",
	Label: "Claude Code",
	Configuration: harnessconfig.Configuration{
		ValidateNativeConfig: validateNativeConfig,
		Providers:            []harnessconfig.Provider{{Protocol: "anthropic"}},
		// CLAUDE_CODE_MAX_OUTPUT_TOKENS, which native caps at the model's limit.
		MaxOutputTokens: true,
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
	// The SDK takes effort low to max. Its thinking blocks are the reasoning
	// text, so a summary request takes only auto; there is no concise or
	// detailed setting. It has no verbosity or service tier. Native REPL runs
	// tool calls inside model-written code, outside the root tool_use and
	// tool_result records that function receipts and tool observations
	// read, so enabling programmatic tool calling is rejected. Native WebSearch and
	// WebFetch are live and have no cached mode or settings: disabled removes them,
	// and live and an absent control keep them on workspace placements. On none,
	// live adds only WebSearch. Medium is the official default verbosity and needs
	// no setting.
	Execution: harnessconfig.ExecutionOptions{
		WebSearchModes:          []string{"disabled", "live"},
		ReasoningEfforts:        []string{"low", "medium", "high", "xhigh", "max"},
		ReasoningSummaries:      []string{"auto"},
		ProgrammaticToolCalling: []bool{false},
		TextVerbosities:         []string{"medium"},
	},
	Placements:             []string{"none", "openai_hosted", "self_hosted"},
	MCPOrigins:             []string{"service", "environment"},
	WhitespaceOnlyText:     proto.CapabilitySupported,
	ValidateConfiguration:  validateConfiguration,
	ValidateTools:          validateTools,
	ValidateMessageInput:   validateMessageInput,
	ValidateFunctionResult: validateFunctionResult,
}

var mcpLabel = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateConfiguration holds the packaged adapter's execution limits on a saved
// Agent and Environment, not saved-Agent schema rules.
func validateConfiguration(admission harnessconfig.Admission) error {
	if admission.TextFormat == "json_schema" {
		if err := proto.ValidateBinary64Schema(admission.TextSchema); err != nil {
			return err
		}
	}
	for _, raw := range admission.Tools {
		var tool struct {
			Type      string `json:"type"`
			Transport struct {
				Type string `json:"type"`
				Cwd  string `json:"cwd"`
			} `json:"transport"`
		}
		// Native starts stdio servers in the workspace and has no cwd setting.
		if json.Unmarshal(raw, &tool) == nil && tool.Type == "mcp" && tool.Transport.Type == "stdio" && admission.WorkspaceDirectory != "" &&
			filepath.Clean(tool.Transport.Cwd) != filepath.Clean(admission.WorkspaceDirectory) {
			return errors.New("The configured engine starts MCP stdio servers in the workspace directory; set cwd to it.")
		}
	}
	return nil
}

// validateTools applies execution limits of the packaged adapter. Shared
// resolution still owns URL, transport and credential-binding validation.
func validateTools(tools []proto.FunctionTool, servers []proto.MCPHTTPServer) error {
	for _, server := range servers {
		if !mcpLabel.MatchString(server.ServerLabel) || server.ServerLabel == "functions" || strings.Contains(server.ServerURL, "#") {
			return errors.New("The configured engine does not support this HTTP MCP declaration.")
		}
		// Native hides unselected tools only for HTTP servers, which the adapter lists itself.
		if server.Stdio != nil && server.AllowedTools != nil {
			return errors.New("The configured engine requires allowed_tools=null for MCP stdio servers.")
		}
		if ExpandsEnvironment(server) {
			return errors.New("The configured engine does not support ${ in MCP URLs or header values.")
		}
	}
	return harnessconfig.RequireObjectSchemas(tools)
}

func validateFunctionResult(result proto.FunctionResultPayload) error {
	for _, part := range result.Content {
		if part.Type == "input_image" && (!result.Success || !inlineImage(*part.ImageURL)) {
			return harnessconfig.ErrInvalidInput
		}
	}
	return nil
}

// A user message image that is not a data URL becomes a native URL source.
func validateMessageInput(input proto.MessageInput) error {
	for _, message := range input {
		for _, part := range message.Content {
			if part.Type == "input_image" && strings.HasPrefix(*part.ImageURL, "data:") && !inlineImage(*part.ImageURL) {
				return harnessconfig.ErrInvalidInput
			}
		}
	}
	return nil
}

// inlineImage reports canonical base64 in a media type the pinned SDK declares.
func inlineImage(url string) bool {
	header, encoded, _ := strings.Cut(url, ",")
	data, err := base64.StdEncoding.DecodeString(encoded)
	switch header {
	case "data:image/png;base64", "data:image/jpeg;base64", "data:image/gif;base64", "data:image/webp;base64":
		return err == nil && encoded != "" && base64.StdEncoding.EncodeToString(data) == encoded
	}
	return false
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

// ExpandsEnvironment reports a URL or header value the native client would
// expand from its own environment, which holds Core-held credentials.
func ExpandsEnvironment(server proto.MCPHTTPServer) bool {
	if strings.Contains(server.ServerURL, "${") {
		return true
	}
	for _, value := range server.Headers {
		if strings.Contains(value, "${") {
			return true
		}
	}
	return false
}
