package cli

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

const claudeSDKEntrypointEnv = "OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"
const claudeSDKNodeEnv = "OAC_RUNTIME_CLAUDE_SDK_NODE"

type claudeSDKDiscovery struct {
	Info   proto.SupportedAgentKind
	Config claudesdk.Config
}

func discoverClaudeSDK(parent context.Context, rc *runContext, profile string, check func(context.Context, claudesdk.Config) (claudesdk.RuntimeInfo, error)) *claudeSDKDiscovery {
	entrypoint := os.Getenv(claudeSDKEntrypointEnv)
	if entrypoint == "" {
		return nil
	}
	out := &claudeSDKDiscovery{Info: proto.SupportedAgentKind{Kind: "claude_sdk", Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:           proto.CapabilityUnsupported,
		Streaming:                      proto.CapabilitySupported,
		Permissions:                    proto.CapabilityUnsupported,
		Usage:                          proto.CapabilitySupported,
		Resume:                         proto.CapabilitySupported,
		NativeSessionRecovery:          proto.CapabilityUnsupported,
		WorkspaceAuthoring:             proto.CapabilityUnsupported,
		Steering:                       proto.CapabilitySupported,
		MessageItems:                   proto.CapabilitySupported,
		ToolObservations:               proto.CapabilitySupported,
		EnvironmentNone:                proto.CapabilitySupported,
		LocalEnvironment:               proto.CapabilityUnsupported,
		Preparation:                    proto.CapabilityUnsupported,
		WorkspaceReadPreparation:       proto.CapabilityUnsupported,
		WorkspaceOutputExport:          proto.CapabilityUnsupported,
		ProgrammaticToolCallingDisable: proto.CapabilitySupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		ExecutionControls:              proto.CapabilitySupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilityUnsupported,
		FunctionResultImages:           proto.CapabilityUnsupported,
		SubagentControl:                proto.CapabilitySupported,
		DurableInputReceipts:           proto.CapabilitySupported,
		DurableTurns:                   proto.CapabilitySupported,
		FunctionTools:                  proto.CapabilitySupported,
		MCPHTTPTools:                   proto.CapabilityUnsupported,
		MCPHTTPRequired:                proto.CapabilityUnsupported,
		MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
	}}}
	fail := func(err error) *claudeSDKDiscovery {
		fmt.Fprintf(rc.stderr, "oac-daemon: configured Claude SDK runtime unavailable: %v\n", err)
		return out
	}
	if !filepath.IsAbs(entrypoint) {
		return fail(fmt.Errorf("%s must be absolute", claudeSDKEntrypointEnv))
	}
	profileDir, err := paths.ProfileDir(profile)
	if err != nil {
		return fail(err)
	}
	if !filepath.IsAbs(profileDir) {
		return fail(fmt.Errorf("Claude SDK state requires an absolute OAC_RUNTIME_HOME"))
	}
	node := os.Getenv(claudeSDKNodeEnv)
	if node == "" {
		node = "node"
	}
	node, err = exec.LookPath(node)
	if err != nil {
		return fail(fmt.Errorf("Claude SDK Node executable is unavailable"))
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return fail(err)
	}
	out.Config = claudesdk.Config{Node: node, Entrypoint: entrypoint, StateDir: filepath.Join(profileDir, "runtime", "claude-sdk")}
	binding, err := localworkspace.Load()
	if err != nil {
		return fail(err)
	}
	if binding != nil {
		root, err := paths.Root()
		if err != nil {
			return fail(err)
		}
		out.Config.Node, err = filepath.EvalSymlinks(node)
		if err != nil {
			return fail(err)
		}
		out.Config, err = claudesdk.ConfigureLocal(out.Config, root, os.Getenv("OAC_RUNTIME_WORKSPACE"), binding.NetworkPolicy())
		if err != nil {
			return fail(err)
		}
	}
	if check == nil {
		check = claudesdk.CheckRuntime
	}
	info, err := check(parent, out.Config)
	if err != nil {
		return fail(err)
	}
	if out.Config.Workspace != nil {
		if !info.SupportsLocalRuntime() {
			return fail(fmt.Errorf("Claude SDK bundle does not support the local Runtime contract"))
		}
		caps := &out.Info.Capabilities
		caps.EnvironmentNone, caps.FunctionTools = proto.CapabilityUnsupported, proto.CapabilityFromBool(info.SupportsWorkspaceFunctions())
		caps.Preparation, caps.LocalEnvironment = proto.CapabilitySupported, proto.CapabilitySupported
		caps.WorkspaceReadPreparation, caps.NativeSessionRecovery = proto.CapabilitySupported, proto.CapabilitySupported
	}
	out.Info.Available, out.Info.Version = true, info.SDK
	out.Info.Capabilities.MessageImages = proto.CapabilityFromBool(info.SupportsMessageImages())
	out.Info.Capabilities.FunctionResultImages = proto.CapabilityFromBool(info.SupportsFunctionResultImages())
	out.Info.Capabilities.ToolSearch = proto.CapabilityFromBool(info.SupportsToolSearch())
	if out.Config.Workspace != nil {
		out.Info.Capabilities.ToolSearch = proto.CapabilityFromBool(info.SupportsWorkspaceToolSearch())
	}
	out.Info.Capabilities.StructuredOutput = proto.CapabilityFromBool(info.SupportsStructuredOutput())
	if out.Config.Workspace != nil {
		out.Info.Capabilities.StructuredOutput = proto.CapabilityFromBool(info.SupportsWorkspaceStructuredOutput())
	}
	out.Info.Capabilities.SubagentObservations = proto.CapabilityFromBool(info.SupportsSubagents())
	out.Info.Capabilities.MCPHTTPTools = proto.CapabilityFromBool(info.SupportsHTTPMCP())
	out.Info.Capabilities.MCPHTTPBearerAuth = proto.CapabilityFromBool(info.SupportsHTTPMCPBearer())
	out.Info.Capabilities.MCPHTTPRequired = proto.CapabilityFromBool(info.SupportsHTTPMCPRequired())
	if out.Config.Workspace != nil && !info.SupportsWorkspaceMCP() {
		out.Info.Capabilities.MCPHTTPTools, out.Info.Capabilities.MCPHTTPBearerAuth = proto.CapabilityUnsupported, proto.CapabilityUnsupported
		out.Info.Capabilities.MCPHTTPRequired = proto.CapabilityUnsupported
	}
	fmt.Fprintf(rc.stdout, "Claude SDK preflight ok (SDK %s, %s)\n", info.SDK, info.Native)
	return out
}

func registerClaudeSDK(registry *agent.Registry, discovery *claudeSDKDiscovery) {
	if discovery == nil {
		return
	}
	factory := claudesdk.NewFactory(discovery.Config)
	if !discovery.Info.Available {
		factory = func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, fmt.Errorf("claude_sdk: configured runtime is unavailable")
		}
	}
	registry.RegisterKind(discovery.Info, builtin.Configuration("claude_sdk"), factory)
	if discovery.Info.Available {
		registry.RegisterExecutor("claude_sdk", claudesdk.NewExecutorFactory(discovery.Config))
	}
	if discovery.Info.Available && discovery.Info.Capabilities.LocalEnvironment.IsSupported() {
		registry.RegisterPreparation("claude_sdk", true, claudesdk.NewPreparationFactory(discovery.Config))
	}
}
