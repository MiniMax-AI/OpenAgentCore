package harnessconfig

import (
	"encoding/json"
	"errors"
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
	Model string
	// Reasoning reports an explicit effort or summary.
	Reasoning     bool
	ServiceTier   string
	TextVerbosity string
	TextFormat    string
	TextSchema    json.RawMessage
	MultiAgent    bool
	Tools         []json.RawMessage
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
