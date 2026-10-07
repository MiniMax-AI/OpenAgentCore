package claudesdk

import configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

const claudeSDKEntrypointEnv = "OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"
const claudeSDKNodeEnv = "OAC_RUNTIME_CLAUDE_SDK_NODE"

// Declaration owns Claude SDK discovery, configuration and execution factories.
var Declaration = agent.Declaration{Info: proto.SupportedAgentKind{Kind: "claude_sdk", Capabilities: proto.AgentKindCapabilities{
	SubagentObservations:           proto.CapabilityUnsupported,
	NativeSessionRecovery:          proto.CapabilityUnsupported,
	EnvironmentNone:                proto.CapabilitySupported,
	LocalEnvironment:               proto.CapabilityUnsupported,
	WorkspaceReadPreparation:       proto.CapabilityUnsupported,
	WorkspaceOutputExport:          proto.CapabilityUnsupported,
	ProgrammaticToolCallingDisable: proto.CapabilitySupported,
	WebSearchControl:               proto.CapabilityUnsupported,
	TextVerbosity:                  proto.CapabilityUnsupported,
	StructuredOutput:               proto.CapabilityUnsupported,
	ToolSearch:                     proto.CapabilityUnsupported,
	MessageImages:                  proto.CapabilityUnsupported,
	FunctionResultImages:           proto.CapabilityUnsupported,
	SubagentControl:                proto.CapabilitySupported,
	FunctionTools:                  proto.CapabilitySupported,
	MCPHTTPTools:                   proto.CapabilityUnsupported,
	MCPHTTPRequired:                proto.CapabilityUnsupported,
	MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
}}, Configuration: configuration.Configuration(), Discover: discover}

func discover(ctx context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
	return discoverWithCheck(ctx, options, info, CheckRuntime)
}
func discoverWithCheck(parent context.Context, options agent.DiscoveryOptions, descriptor proto.SupportedAgentKind, check func(context.Context, Config) (RuntimeInfo, error)) *agent.Runtime {
	entrypoint := os.Getenv(claudeSDKEntrypointEnv)
	if entrypoint == "" {
		return nil
	}
	out := &agent.Runtime{Info: descriptor}
	var config Config

	fail := func(err error) *agent.Runtime {
		fmt.Fprintf(options.Stderr, "oac-daemon: configured Claude SDK runtime unavailable: %v\n", err)
		return out
	}
	if !filepath.IsAbs(entrypoint) {
		return fail(fmt.Errorf("%s must be absolute", claudeSDKEntrypointEnv))
	}
	profileDir, err := paths.ProfileDir(options.Profile)
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
	config = Config{Node: node, Entrypoint: entrypoint, StateDir: filepath.Join(profileDir, "runtime", "claude-sdk")}
	binding, err := localworkspace.Load()
	if err != nil {
		return fail(err)
	}
	if binding != nil {
		root, err := paths.Root()
		if err != nil {
			return fail(err)
		}
		config.Node, err = filepath.EvalSymlinks(node)
		if err != nil {
			return fail(err)
		}
		config, err = ConfigureLocal(config, root, os.Getenv("OAC_RUNTIME_WORKSPACE"), binding.NetworkPolicy())
		if err != nil {
			return fail(err)
		}
	}
	info, err := check(parent, config)
	if err != nil {
		return fail(err)
	}
	caps := &out.Info.Capabilities
	if config.Workspace != nil {
		if !info.SupportsLocalRuntime() {
			return fail(fmt.Errorf("Claude SDK bundle does not support the local Runtime contract"))
		}
		caps.NativeSessionRecovery = proto.CapabilitySupported
	}
	// One declaration holds for every Executor of the install: the workspace
	// bridge, the agent-host view and a Runtime without a workspace, so each
	// feature is its workspace variant, which the others also support.
	out.Info.Available, out.Info.Version = true, info.SDK
	caps.LocalEnvironment = proto.CapabilityFromBool(info.SupportsLocalRuntime())
	caps.FunctionTools = proto.CapabilityFromBool(info.SupportsWorkspaceFunctions())
	caps.MessageImages = proto.CapabilityFromBool(info.SupportsMessageImages())
	caps.FunctionResultImages = proto.CapabilityFromBool(info.SupportsFunctionResultImages())
	caps.ToolSearch = proto.CapabilityFromBool(info.SupportsWorkspaceToolSearch())
	caps.StructuredOutput = proto.CapabilityFromBool(info.SupportsWorkspaceStructuredOutput())
	caps.SubagentObservations = proto.CapabilityFromBool(info.SupportsSubagents())
	caps.MCPHTTPTools = proto.CapabilityFromBool(info.SupportsWorkspaceMCP())
	caps.MCPHTTPBearerAuth = proto.CapabilityFromBool(info.SupportsWorkspaceMCP() && info.SupportsHTTPMCPBearer())
	caps.MCPHTTPRequired = proto.CapabilityFromBool(info.SupportsWorkspaceMCP() && info.SupportsHTTPMCPRequired())
	out.Executor = NewExecutorFactory(config)
	// The view runs the same install; its probe stays on this host.
	if view, err := newView(Config{Node: node, Entrypoint: entrypoint}, info); err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: Claude SDK agent-host view unavailable: %v\n", err)
	} else {
		out.View = view
	}

	fmt.Fprintf(options.Stdout, "Claude SDK preflight ok (SDK %s, %s)\n", info.SDK, info.Native)
	return out
}
