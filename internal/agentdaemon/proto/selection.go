package proto

import (
	"encoding/json"
	"slices"
	"strings"
)

// Feature names one part of a Selection that a Declaration's Conflicts pair.
type Feature string

const (
	FeatureMultiAgent            Feature = "multi_agent"
	FeatureMCP                   Feature = "mcp"
	FeatureToolSearch            Feature = "tool_search"
	FeatureJSONSchema            Feature = "json_schema"
	FeatureInstalledCapabilities Feature = "installed_capabilities"
)

func (f Feature) valid() bool {
	switch f {
	case FeatureMultiAgent, FeatureMCP, FeatureToolSearch, FeatureJSONSchema, FeatureInstalledCapabilities:
		return true
	}
	return false
}

// Selection is the credential-free projection of one execution request that
// ValidateSelection checks against a Declaration. Core builds it from the
// frozen Session configuration and input, the Runtime from the prepared
// request. Zero fields select nothing.
type Selection struct {
	// Environment is "none" or "local", or empty before an Environment is
	// selected.
	Environment           string
	InstalledCapabilities bool
	MultiAgent            bool
	Functions             bool
	DeferredFunctions     bool
	ToolSearch            bool
	// OutputSchema is the schema of a json_schema output format.
	OutputSchema   json.RawMessage
	TextVerbosity  string
	MCP            []SelectedMCP
	Messages       MessageInput
	FunctionResult *FunctionResultPayload
}

// SelectedMCP is one MCP server of a Selection. An Installed server comes
// from the Environment's installed capabilities rather than agent.tools, so
// only the declared label rules and Conflicts apply to it.
type SelectedMCP struct {
	Origin, Label               string
	AllowedTools                *[]string
	Required, Bearer, Installed bool
}

// SelectionError rejects a Selection. Param is the Session configuration path
// it rejects, such as agent.tools; an input rejection has none.
type SelectionError struct {
	Param, Message string
}

func (e *SelectionError) Error() string { return e.Message }

// WhitespaceOnlyTextMessage reports a message that a Harness without
// WhitespaceOnlyText cannot take.
const WhitespaceOnlyTextMessage = "This Session's harness does not accept a message whose text is only whitespace. Include non-whitespace text or an image, or use a harness that supports whitespace-only text."

func (s Selection) has(f Feature) bool {
	switch f {
	case FeatureMultiAgent:
		return s.MultiAgent
	case FeatureMCP:
		return len(s.MCP) > 0
	case FeatureToolSearch:
		return s.ToolSearch
	case FeatureJSONSchema:
		return s.OutputSchema != nil
	case FeatureInstalledCapabilities:
		return s.InstalledCapabilities
	}
	return false
}

var featureParams = map[Feature]string{FeatureMultiAgent: "agent.multi_agent", FeatureMCP: "agent.tools", FeatureToolSearch: "agent.tools",
	FeatureJSONSchema: "agent.text.format", FeatureInstalledCapabilities: "environment"}

// ValidateSelection is the only check of a Harness's declared support, and
// of the common rules that hold for every Harness. Core applies it with the
// static declaration before a Runtime is chosen and with the Runtime's
// narrowed declaration afterwards; the Runtime applies it before the
// Executor factory.
func ValidateSelection(d Declaration, s Selection) error {
	c := d.Capabilities
	reject := func(param, message string) error { return &SelectionError{Param: param, Message: message} }
	selectedMCP := slices.ContainsFunc(s.MCP, func(server SelectedMCP) bool { return !server.Installed })
	switch {
	case s.Environment == "none" && !c.EnvironmentNone.IsSupported():
		return reject("environment", "The harness does not support environment none.")
	case s.Environment == "local" && !c.LocalEnvironment.IsSupported():
		return reject("environment", "The harness does not support this execution environment.")
	case s.MultiAgent && !c.SubagentObservations.IsSupported():
		return reject("agent.multi_agent", "The harness does not support multi-agent execution.")
	case s.MultiAgent && (s.Functions || selectedMCP):
		return reject("agent.multi_agent", "Multi-agent execution does not support function or MCP tools.")
	case s.Functions && !c.FunctionTools.IsSupported():
		return reject("agent.tools", "The harness does not support function tools.")
	case (s.ToolSearch || s.DeferredFunctions) && !c.ToolSearch.IsSupported():
		return reject("agent.tools", "The harness does not support tool search.")
	case s.ToolSearch != s.DeferredFunctions:
		return reject("agent.tools", "Tool search requires deferred functions, and deferred functions require tool search.")
	case s.OutputSchema != nil && !c.StructuredOutput.IsSupported():
		return reject("agent.text.format", "The harness does not support json_schema output.")
	case s.TextVerbosity != "" && s.TextVerbosity != "medium" && !c.TextVerbosity.IsSupported():
		return reject("agent.text.verbosity", "The harness supports medium text verbosity only.")
	}
	if s.OutputSchema != nil && d.Binary64OutputSchema {
		if err := ValidateBinary64Schema(s.OutputSchema); err != nil {
			return reject("agent.text.format", err.Error())
		}
	}
	for _, server := range s.MCP {
		if err := validateMCP(d, s.Environment, server); err != "" {
			if server.Installed {
				return reject("environment", err)
			}
			return reject("agent.tools", err)
		}
	}
	for _, pair := range d.Conflicts {
		if s.has(pair[0]) && s.has(pair[1]) {
			return reject(featureParams[pair[0]], "The harness does not support "+string(pair[0])+" with "+string(pair[1])+".")
		}
	}
	if s.Messages.HasImages() && !c.MessageImages.IsSupported() {
		return reject("", "The harness does not support message images.")
	}
	if !d.WhitespaceOnlyText.IsSupported() {
		for _, message := range s.Messages {
			if !message.meaningful() {
				return reject("", WhitespaceOnlyTextMessage)
			}
		}
	}
	if result := s.FunctionResult; result != nil {
		images := MessageInput{{Content: result.Content}}
		switch {
		case !c.FunctionTools.IsSupported():
			return reject("", "The harness does not support function results.")
		case !images.HasImages():
		case !c.FunctionResultImages.IsSupported():
			return reject("", "The harness does not support function result images.")
		case !result.Success && !d.FailedFunctionResultImages.IsSupported():
			return reject("", "The harness does not support images in a failed function result.")
		case !d.FunctionResultImageURLs.IsSupported() && images.ValidateInlineImages() != nil:
			return reject("", "The harness supports only inline PNG or JPEG function result images.")
		}
	}
	return nil
}

func validateMCP(d Declaration, environment string, server SelectedMCP) string {
	if slices.Contains(d.ReservedMCPLabels, server.Label) || (d.MCPLabel != nil && !d.MCPLabel.MatchString(server.Label)) {
		return "The harness does not support this MCP server label."
	}
	if server.Installed {
		return ""
	}
	switch {
	case server.Origin == "service" && environment != "" && environment != "none":
		return "Service-origin MCP requires environment none."
	case server.Origin == "environment" && environment != "" && environment != "local":
		return "Environment-origin MCP requires a managed or self-hosted execution environment."
	case !d.Capabilities.MCPHTTPTools.IsSupported():
		return "The harness does not support MCP tools."
	case !slices.Contains(d.MCPOrigins, server.Origin):
		return "The harness does not support this MCP connection origin."
	case server.AllowedTools != nil && !d.MCPAllowedTools.IsSupported():
		return "The harness requires allowed_tools null."
	case server.Required && !d.Capabilities.MCPHTTPRequired.IsSupported():
		return "The harness does not support required MCP servers."
	case server.Bearer && !d.Capabilities.MCPHTTPBearerAuth.IsSupported():
		return "The harness does not support authenticated MCP servers."
	}
	if server.AllowedTools != nil && d.MCPToolName != nil {
		for _, name := range *server.AllowedTools {
			if !d.MCPToolName.MatchString(name) {
				return "The harness does not support this MCP tool name."
			}
		}
	}
	return ""
}

// meaningful reports an image or text with a character other than
// whitespace.
func (m InputMessage) meaningful() bool {
	for _, part := range m.Content {
		if part.Type == "input_image" || (part.Text != nil && strings.TrimFunc(*part.Text, blankTextRune) != "") {
			return true
		}
	}
	return false
}

// blankTextRune is the explicit union of Go unicode.IsSpace and ECMAScript
// String.prototype.trim (WhiteSpace and LineTerminator), so admission rejects
// every message a JavaScript Harness would treat as blank.
func blankTextRune(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u0085', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

// Selection projects a request after its Environment owner configured or
// prepared it; installed MCP servers appear only after preparation.
func (r PromptRequestPayload) Selection() Selection {
	s := Selection{MultiAgent: r.ObserveSubagentIdentities, Functions: len(r.FunctionTools) > 0, ToolSearch: r.ToolSearch, Messages: r.Input}
	for _, tool := range r.FunctionTools {
		s.DeferredFunctions = s.DeferredFunctions || tool.DeferLoading
	}
	if r.DisableExecutionEnvironment {
		s.Environment = "none"
	}
	if local := r.LocalEnvironment; local != nil {
		s.Environment, s.InstalledCapabilities = "local", local.Capabilities
		for _, installed := range local.MCP {
			s.MCP = append(s.MCP, SelectedMCP{Origin: "environment", Label: installed.Server.Name, Installed: true})
		}
	}
	if c := r.ExecutionControls; c != nil {
		s.TextVerbosity = c.TextVerbosity
		if c.OutputFormat != nil {
			s.OutputSchema = c.OutputFormat.Schema
		}
	}
	if r.MCPHTTPServers != nil {
		for _, server := range *r.MCPHTTPServers {
			s.MCP = append(s.MCP, SelectedMCP{Origin: server.ConnectionOrigin, Label: server.ServerLabel, AllowedTools: server.AllowedTools, Required: server.Required, Bearer: server.BearerToken != nil})
		}
	}
	return s
}
