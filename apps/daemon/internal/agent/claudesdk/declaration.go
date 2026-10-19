package claudesdk

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
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
)

const claudeSDKEntrypointEnv = "OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT"
const claudeSDKNodeEnv = "OAC_RUNTIME_CLAUDE_SDK_NODE"

// Declaration binds the shared Claude Code declaration to discovery and execution factories.
var Declaration = agent.Declaration{Declaration: declaration.Declaration, Discover: discover}

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
		return agent.Unavailable(descriptor)
	}
	if !filepath.IsAbs(entrypoint) {
		return fail(fmt.Errorf("%s must be absolute", claudeSDKEntrypointEnv))
	}
	if err := paths.ValidateProfile(options.Profile); err != nil {
		return fail(err)
	}
	if !filepath.IsAbs(options.RuntimeRoot) || filepath.Clean(options.RuntimeRoot) != options.RuntimeRoot {
		return fail(fmt.Errorf("Claude SDK requires an explicit Runtime root"))
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
	if !filepath.IsAbs(options.StateRoot) || filepath.Clean(options.StateRoot) != options.StateRoot {
		return fail(fmt.Errorf("Claude SDK requires an explicit state root"))
	}
	config = Config{Node: node, Entrypoint: entrypoint, StateDir: filepath.Join(options.StateRoot, "daemon", options.Profile, "runtime", "claude-sdk")}
	binding, err := localworkspace.Load()
	if err != nil {
		return fail(err)
	}
	if binding != nil {
		config.Node, err = filepath.EvalSymlinks(node)
		if err != nil {
			return fail(err)
		}
		config, err = ConfigureLocal(config, options.StateRoot, options.RuntimeRoot, os.Getenv("OAC_RUNTIME_WORKSPACE"))
		if err != nil {
			return fail(err)
		}
	}
	info, err := check(parent, config)
	if err != nil {
		return fail(err)
	}
	local := config.Workspace != nil
	if local && !info.SupportsLocalRuntime() {
		return fail(fmt.Errorf("Claude SDK bundle does not support the local Runtime contract"))
	}
	out.Info.Available, out.Info.Version = true, info.SDK
	caps := &out.Info.Capabilities
	// The local workspace layout replaces environment:none.
	agent.Narrow(&caps.EnvironmentNone, !local)
	for _, capability := range []*proto.CapabilitySupport{&caps.LocalEnvironment, &caps.WorkspaceReadPreparation, &caps.WorkspaceOutputExport, &caps.NativeSessionRecovery} {
		agent.Narrow(capability, local)
	}
	agent.Narrow(&caps.RetainedNativeHistory, local && info.SDK == "0.3.269" && info.Native == "2.1.269 (Claude Code)")
	agent.Narrow(&caps.FunctionTools, !local || info.SupportsWorkspaceFunctions())
	agent.Narrow(&caps.MessageImages, info.SupportsMessageImages())
	agent.Narrow(&caps.FunctionResultImages, info.SupportsFunctionResultImages())
	agent.Narrow(&caps.ToolSearch, info.SupportsToolSearch() && (!local || info.SupportsWorkspaceToolSearch()))
	agent.Narrow(&caps.StructuredOutput, info.SupportsStructuredOutput() && (!local || info.SupportsWorkspaceStructuredOutput()))
	agent.Narrow(&caps.SubagentObservations, info.SupportsSubagents())
	mcp := !local || info.SupportsWorkspaceMCP()
	agent.Narrow(&caps.MCPHTTPTools, mcp && info.SupportsHTTPMCP())
	agent.Narrow(&caps.MCPHTTPBearerAuth, mcp && info.SupportsHTTPMCPBearer())
	agent.Narrow(&caps.MCPHTTPRequired, mcp && info.SupportsHTTPMCPRequired())
	out.Executor = NewExecutorFactory(config)

	fmt.Fprintf(options.Stdout, "Claude SDK preflight ok (SDK %s, %s)\n", info.SDK, info.Native)
	return out
}
