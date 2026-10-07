// Package codex owns the qualified Codex provider configuration declaration.
package codex

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func Configuration() harnessconfig.Configuration {
	s, u := proto.CapabilitySupported, proto.CapabilityUnsupported
	return harnessconfig.Configuration{ValidateNativeConfig: validateNativeConfig, Providers: []harnessconfig.Provider{
		{Protocol: "responses"},
	}, Declaration: proto.Declaration{
		Capabilities: proto.AgentKindCapabilities{
			SubagentObservations: s, NativeSessionRecovery: s, EnvironmentNone: s, LocalEnvironment: s,
			TextVerbosity: s, StructuredOutput: u, ToolSearch: u, MessageImages: s, FunctionResultImages: s,
			FunctionTools: s, MCPHTTPTools: s, MCPHTTPRequired: s, MCPHTTPBearerAuth: s,
		},
		WhitespaceOnlyText: s, FunctionResultImageURLs: s, FailedFunctionResultImages: s, MCPAllowedTools: s,
		MCPOrigins:        []string{"service", "environment"},
		ReservedMCPLabels: []string{"codex_apps"},
	}}
}
