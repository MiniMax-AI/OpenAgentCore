// Package mcode owns the qualified MiniMax Code provider declaration.
package mcode

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func Configuration() harnessconfig.Configuration {
	s, u := proto.CapabilitySupported, proto.CapabilityUnsupported
	return harnessconfig.Configuration{Providers: []harnessconfig.Provider{
		{Protocol: "anthropic", RequiresTokenLimits: true},
		{Protocol: "responses", RequiresTokenLimits: true},
		{Protocol: "chat_completions", RequiresTokenLimits: true},
	}, Declaration: proto.Declaration{
		Capabilities: proto.AgentKindCapabilities{
			SubagentObservations: s, NativeSessionRecovery: u, EnvironmentNone: s, LocalEnvironment: s,
			TextVerbosity: u, StructuredOutput: u, ToolSearch: u, MessageImages: u, FunctionResultImages: u,
			FunctionTools: u, MCPHTTPTools: s, MCPHTTPRequired: u, MCPHTTPBearerAuth: s,
		},
		// The native runtime refuses a prompt without non-whitespace text.
		WhitespaceOnlyText: u, FunctionResultImageURLs: u, FailedFunctionResultImages: u, MCPAllowedTools: u,
		MCPOrigins:        []string{"environment"},
		ReservedMCPLabels: []string{"oac_workspace"},
	}}
}
