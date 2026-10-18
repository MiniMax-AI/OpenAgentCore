package harnessconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// ErrInvalidInput marks a configuration a Harness declaration rejects. Core
// reports it as an invalid request, without echoing submitted values.
var ErrInvalidInput = errors.New("invalid harness configuration")

// Admission is the saved Agent and Environment a Harness declaration validates.
// Core projects its public types into it, so declarations stay independent of
// the public API package.
type Admission struct {
	Model      string
	TextFormat string
	TextSchema json.RawMessage
	MultiAgent bool
	Tools      []json.RawMessage
	// Environment is nil when the request names none.
	Environment *AdmissionEnvironment
}

// AdmissionEnvironment carries the placement and whether the Environment adds
// Skills, Plugins or capability directories.
type AdmissionEnvironment struct {
	Type       string
	Extensions bool
}

// ValidateMCPOrigins preserves outbound authority independently of Harness names.
// Workspace connections never stand in for service-network connections.
func (d Declaration) ValidateMCPOrigins(environment *AdmissionEnvironment, servers []proto.MCPHTTPServer) error {
	for _, server := range servers {
		switch server.ConnectionOrigin {
		case "service":
			if environment == nil || environment.Type != "none" {
				return errors.New("Service-origin MCP requires the service-side environment:none profile.")
			}
		case "environment":
			if environment == nil || (environment.Type != "openai_hosted" && environment.Type != "self_hosted") {
				return errors.New("Environment-origin MCP requires a managed or self-hosted execution Environment.")
			}
		default:
			return errors.New("MCP requires an explicit connection origin.")
		}
		if !slices.Contains(d.MCPOrigins, server.ConnectionOrigin) {
			return errors.New("The configured engine does not support this MCP connection origin.")
		}
	}
	return nil
}

// RejectSubagentTools rejects the declared tool types that a Harness cannot
// combine with Subagents.
func RejectSubagentTools(admission Admission, unsupported ...string) error {
	if !admission.MultiAgent {
		return nil
	}
	for _, raw := range admission.Tools {
		var tool struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			return ErrInvalidInput
		}
		if slices.Contains(unsupported, tool.Type) {
			return errors.New("The configured Subagent profile does not support this tool combination.")
		}
	}
	return nil
}

// ExecutionOptions lists the official execution option values a Harness maps to
// a native setting. An empty set declares the option unsupported. Absent
// options need no declaration: they follow the Harness's native default. Auto
// service tier means unset, so it is always accepted.
type ExecutionOptions struct {
	// WebSearchModes are the supported web_search modes: disabled, cached, live.
	WebSearchModes []string
	// WebSearchSettings reports native homes for context_size, allowed_domains
	// and location.
	WebSearchSettings bool
	// ReasoningEfforts and ReasoningSummaries are the supported reasoning values.
	ReasoningEfforts, ReasoningSummaries []string
	// ServiceTiers are the supported tiers other than auto.
	ServiceTiers []string
	// ProgrammaticToolCalling lists the supported enabled values.
	ProgrammaticToolCalling []bool
	// TextVerbosities are the supported text verbosity levels.
	TextVerbosities []string
}

func (o ExecutionOptions) Clone() ExecutionOptions {
	o.WebSearchModes = slices.Clone(o.WebSearchModes)
	o.ReasoningEfforts = slices.Clone(o.ReasoningEfforts)
	o.ReasoningSummaries = slices.Clone(o.ReasoningSummaries)
	o.ServiceTiers = slices.Clone(o.ServiceTiers)
	o.ProgrammaticToolCalling = slices.Clone(o.ProgrammaticToolCalling)
	o.TextVerbosities = slices.Clone(o.TextVerbosities)
	return o
}

// ValidateExecutionControls rejects a user-set or default value the Harness
// does not declare. Core admission and the Runtime call it with the same
// controls, so neither keeps a second list. Messages name the field and never
// echo configuration other than the rejected value.
func (d Declaration) ValidateExecutionControls(controls *proto.ExecutionControls) error {
	if controls == nil {
		return nil
	}
	o := d.Execution
	reject := func(field, value string) error {
		return fmt.Errorf("%s %q is not supported by this Harness.", field, value)
	}
	if search := controls.WebSearch; search != nil {
		if !slices.Contains(o.WebSearchModes, search.Mode) {
			return reject("web_search.mode", search.Mode)
		}
		if !o.WebSearchSettings && (search.ContextSize != "medium" || search.AllowedDomains != nil || search.Location != nil) {
			return errors.New("web_search context_size, allowed_domains and location are not supported by this Harness.")
		}
	}
	if enabled := controls.ProgrammaticToolCalling; enabled != nil && !slices.Contains(o.ProgrammaticToolCalling, *enabled) {
		return fmt.Errorf("programmatic_tool_calling.enabled=%t is not supported by this Harness.", *enabled)
	}
	if v := controls.ReasoningEffort; v != "" && !slices.Contains(o.ReasoningEfforts, v) {
		return reject("reasoning.effort", v)
	}
	if v := controls.ReasoningSummary; v != "" && !slices.Contains(o.ReasoningSummaries, v) {
		return reject("reasoning.summary", v)
	}
	if v := controls.ServiceTier; v != "" && !slices.Contains(o.ServiceTiers, v) {
		return reject("service_tier", v)
	}
	if v := controls.TextVerbosity; v != "" && !slices.Contains(o.TextVerbosities, v) {
		return reject("text.verbosity", v)
	}
	return nil
}
