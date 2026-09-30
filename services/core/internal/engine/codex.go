package engine

import (
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func codexProfile() Profile {
	return Profile{
		ProgrammaticToolCallingDisable: proto.CapabilitySupported,
		Placements:                     []string{"none", "self_hosted", "openai_hosted"},
		MCPOrigins:                     []string{"service", "environment"},
		MessageImages:                  proto.CapabilitySupported,
		WhitespaceOnlyText:             proto.CapabilitySupported,
		WebSearchControl:               proto.CapabilitySupported,
		TextVerbosity:                  proto.CapabilitySupported,
		MCPBearer:                      proto.CapabilitySupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		ConfigurationValidation:        AdditionalValidation,
		ToolsValidation:                CommonValidationOnly,
		FunctionResultValidation:       CommonValidationOnly,
		ValidateConfiguration: func(agent v1.Agent, _ *v1.Environment) error {
			return rejectSubagentTools(agent, "function", "mcp")
		},
	}
}
