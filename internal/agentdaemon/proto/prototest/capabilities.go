// Package prototest provides explicit capability fixtures and the shared wire
// scenarios for Core–Runtime contract tests.
package prototest

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"reflect"
)

// Capabilities declares the current contract, then applies explicit overrides.
// List fields individually: a future field must remain unspecified until this
// fixture is deliberately updated, so declaration validation fails on omission.
func Capabilities(overrides proto.AgentKindCapabilities) proto.AgentKindCapabilities {
	c := proto.AgentKindCapabilities{
		SubagentObservations:           proto.CapabilityUnsupported,
		NativeSessionRecovery:          proto.CapabilityUnsupported,
		MessageItems:                   proto.CapabilityUnsupported,
		EnvironmentNone:                proto.CapabilityUnsupported,
		LocalEnvironment:               proto.CapabilityUnsupported,
		WorkspaceReadPreparation:       proto.CapabilityUnsupported,
		WorkspaceOutputExport:          proto.CapabilityUnsupported,
		ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilityUnsupported,
		FunctionResultImages:           proto.CapabilityUnsupported,
		SubagentControl:                proto.CapabilityUnsupported,
		FunctionTools:                  proto.CapabilityUnsupported,
		MCPHTTPTools:                   proto.CapabilityUnsupported,
		MCPHTTPRequired:                proto.CapabilityUnsupported,
		MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
	}
	dst, src := reflect.ValueOf(&c).Elem(), reflect.ValueOf(overrides)
	for i := 0; i < src.NumField(); i++ {
		if !src.Field(i).IsZero() {
			dst.Field(i).Set(src.Field(i))
		}
	}
	if err := c.ValidateDeclaration(); err != nil {
		panic(err)
	}
	return c
}
