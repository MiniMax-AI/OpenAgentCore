package engine

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func piProfile() Profile {
	return Profile{
		RetainedNativeHistory:          proto.CapabilitySupported,
		ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
		Placements:                     []string{"self_hosted"},
		MCPOrigins:                     []string{"environment"},
		MessageImages:                  proto.CapabilityUnsupported,
		WhitespaceOnlyText:             proto.CapabilityUnsupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		MCPBearer:                      proto.CapabilityUnsupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		ConfigurationValidation:        CommonValidationOnly,
		ToolsValidation:                CommonValidationOnly,
		FunctionResultValidation:       CommonValidationOnly,
	}
}
