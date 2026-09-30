// Package enginetest supplies explicit declarations for admission fixtures.
// Production profiles must decide every field in their own constructors.
package enginetest

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
)

// Profile qualifies only common text admission by default. The literal is
// intentionally exhaustive; new fields stay unspecified until decided here.
func Profile(change func(*engine.Profile)) engine.Profile {
	p := engine.Profile{
		ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		MCPBearer:                      proto.CapabilityUnsupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilityUnsupported,
		WhitespaceOnlyText:             proto.CapabilityUnsupported,
		Placements:                     []string{"none"},
		MCPOrigins:                     []string{},
		ConfigurationValidation:        engine.CommonValidationOnly,
		ToolsValidation:                engine.CommonValidationOnly,
		FunctionResultValidation:       engine.CommonValidationOnly,
	}
	if change != nil {
		change(&p)
	}
	return p
}
