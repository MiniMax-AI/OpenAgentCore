// Package claudesdk owns the qualified Claude SDK provider declaration.
package claudesdk

import (
	"regexp"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func Configuration() harnessconfig.Configuration {
	s, u := proto.CapabilitySupported, proto.CapabilityUnsupported
	return harnessconfig.Configuration{ValidateNativeConfig: validateNativeConfig, Providers: []harnessconfig.Provider{
		{Protocol: "anthropic"},
	}, Declaration: proto.Declaration{
		Capabilities: proto.AgentKindCapabilities{
			SubagentObservations: s, NativeSessionRecovery: u, EnvironmentNone: s, LocalEnvironment: s,
			TextVerbosity: u, StructuredOutput: s, ToolSearch: s, MessageImages: s, FunctionResultImages: s,
			FunctionTools: s, MCPHTTPTools: s, MCPHTTPRequired: s, MCPHTTPBearerAuth: s,
		},
		// The bridge and Anthropic-compatible providers reject text without a
		// non-whitespace character, and tool results carry only inline images.
		WhitespaceOnlyText: u, FunctionResultImageURLs: u, FailedFunctionResultImages: u, MCPAllowedTools: s,
		MCPOrigins:        []string{"service", "environment"},
		ReservedMCPLabels: []string{"functions"},
		// The SDK names MCP tools mcp__<label>__<tool>.
		MCPLabel:             regexp.MustCompile(`^[a-zA-Z0-9_-]+$`),
		MCPToolName:          regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`),
		Binary64OutputSchema: true,
		// Multi-agent execution with agent.tools MCP is a common rule; the
		// conflict adds the Environment's installed MCP servers.
		Conflicts: [][2]proto.Feature{
			{proto.FeatureMultiAgent, proto.FeatureMCP},
			{proto.FeatureJSONSchema, proto.FeatureMultiAgent}, {proto.FeatureJSONSchema, proto.FeatureMCP},
			{proto.FeatureJSONSchema, proto.FeatureInstalledCapabilities}, {proto.FeatureJSONSchema, proto.FeatureToolSearch},
			{proto.FeatureToolSearch, proto.FeatureMCP}, {proto.FeatureToolSearch, proto.FeatureInstalledCapabilities},
		},
	}}
}
