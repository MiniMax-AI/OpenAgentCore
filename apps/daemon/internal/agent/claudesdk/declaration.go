package claudesdk

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
)

const claudeSDKEntrypointEnv = "OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"
const claudeSDKNodeEnv = "OAC_RUNTIME_CLAUDE_SDK_NODE"

// Declaration owns Claude SDK discovery, configuration and the agent-host
// view. Discovery narrows the declared support to what the installed bundle
// serves.
var Declaration = agent.Declaration{Info: proto.SupportedAgentKind{Kind: "claude_sdk", Capabilities: configuration.Configuration().Declaration.Capabilities},
	Configuration: configuration.Configuration(), Discover: discover}

func discover(ctx context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
	return discoverWithCheck(ctx, options, info, CheckRuntime)
}
func discoverWithCheck(parent context.Context, options agent.DiscoveryOptions, descriptor proto.SupportedAgentKind, check func(context.Context, Config) (RuntimeInfo, error)) *agent.Runtime {
	entrypoint := os.Getenv(claudeSDKEntrypointEnv)
	if entrypoint == "" {
		return nil
	}
	out := &agent.Runtime{Info: descriptor}
	fail := func(err error) *agent.Runtime {
		fmt.Fprintf(options.Stderr, "oac-daemon: configured Claude SDK runtime unavailable: %v\n", err)
		return out
	}
	if !filepath.IsAbs(entrypoint) {
		return fail(fmt.Errorf("%s must be absolute", claudeSDKEntrypointEnv))
	}
	node := os.Getenv(claudeSDKNodeEnv)
	if node == "" {
		node = "node"
	}
	node, err := exec.LookPath(node)
	if err != nil {
		return fail(fmt.Errorf("Claude SDK Node executable is unavailable"))
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return fail(err)
	}
	config := Config{Node: node, Entrypoint: entrypoint}
	info, err := check(parent, config)
	if err != nil {
		return fail(err)
	}
	caps := &out.Info.Capabilities
	// The view runs the bridge in workspace mode, and without a workspace for
	// environment none, so each feature is its workspace variant, which
	// environment none also supports.
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
	// The view runs the probed install on this host.
	if view, err := newView(config, info); err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: Claude SDK agent-host view unavailable: %v\n", err)
	} else {
		out.View = view
	}

	fmt.Fprintf(options.Stdout, "Claude SDK preflight ok (SDK %s, %s)\n", info.SDK, info.Native)
	return out
}
