package mcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/viewloader"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// In an agent-host view, node runs the CLI from the closure and the native
// data directory lives in the Session home. The workspace worker and the
// Subagent history reader run beside the CLI in the same view; bash, rg and
// git run in the sandbox.

// Closure names and Session home directories.
const (
	viewNodeMount    = "node"
	viewHarnessMount = "mcode-harness"
	viewDataName     = "data"
	viewTempName     = "tmp"
	viewPIName       = "pi-agent"
)

// viewInstall is the trusted MiniMax Code install as a view presents it.
type viewInstall struct {
	closure []agent.ViewMount
	loader  viewloader.Fragment
	// node, cli, bridge and assets are view paths: assets holds the CLI's
	// builtin skills and agents.
	node, cli, bridge, assets string
}

func discoverView(options agent.DiscoveryOptions) *agent.View {
	view, err := findView()
	if err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: mcode agent-host view unavailable: %v\n", err)
		return nil
	}
	return view
}

func findView() (*agent.View, error) {
	programs, err := findPrograms()
	if err != nil {
		return nil, err
	}
	node, err := filepath.EvalSymlinks(programs.node)
	if err != nil {
		return nil, err
	}
	loader, err := viewloader.For(node)
	if err != nil {
		return nil, err
	}
	install, err := newViewInstall(node, programs.binary, programs.bridge, loader)
	if err != nil {
		return nil, err
	}
	view := install.view()
	if err := view.Validate(); err != nil {
		return nil, err
	}
	return &view, nil
}

// newViewInstall presents node's directory and the harness directory holding
// the bridge and the CLI as the closure, with loader for a dynamic node. node
// is a resolved host path.
func newViewInstall(node, cli, bridge string, loader viewloader.Fragment) (viewInstall, error) {
	i := viewInstall{loader: loader}
	if !filepath.IsAbs(bridge) {
		return i, errors.New("the workspace bridge is not configured")
	}
	harness, err := filepath.EvalSymlinks(filepath.Dir(bridge))
	if err != nil {
		return i, err
	}
	nodeMount := agent.ViewMount{Name: viewNodeMount, HostDir: filepath.Dir(node)}
	harnessMount := agent.ViewMount{Name: viewHarnessMount, HostDir: harness}
	i.closure = []agent.ViewMount{nodeMount, harnessMount}
	i.node = path.Join(nodeMount.Path(), filepath.Base(node))
	inHarness := func(file string) (string, error) {
		resolved, err := filepath.EvalSymlinks(file)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(harness, resolved)
		if err != nil || !filepath.IsLocal(rel) {
			return "", fmt.Errorf("%s is outside the harness directory %s", file, harness)
		}
		return path.Join(harnessMount.Path(), filepath.ToSlash(rel)), nil
	}
	if i.cli, err = inHarness(cli); err != nil {
		return i, err
	}
	if i.bridge, err = inHarness(bridge); err != nil {
		return i, err
	}
	i.assets = path.Join(path.Dir(i.cli), "assets")
	resolvedCLI, _ := filepath.EvalSymlinks(cli)
	for _, dir := range []string{"skills", "agents"} {
		if info, err := os.Stat(filepath.Join(filepath.Dir(resolvedCLI), "assets", dir)); err != nil || !info.IsDir() {
			return i, fmt.Errorf("the CLI's builtin %s are missing", dir)
		}
	}
	return i, nil
}

func (i viewInstall) view() agent.View {
	// No forwarded tool needs a Harness variable, so ForwardEnv is empty.
	view := agent.View{
		Closure: slices.Clone(i.closure),
		Masks: []agent.ViewMask{
			// The CLI probes these for builtin assets and its install
			// receipt before its own copy.
			{Path: "/assets", Dir: true}, {Path: "/opt/assets", Dir: true}, {Path: "/install.json"}, {Path: "/opt/install.json"},
			// Node resolves a package the closure lacks, such as the
			// worker's optional ripgrep, from /node_modules.
			{Path: "/node_modules", Dir: true},
		},
		LocalExec: []string{i.node},
		Shims:     []string{"git", "rg"},
		ShimPaths: []string{"/bin/bash"},
		Proxy:     agent.ViewProxyNone,
		Capabilities: agent.ViewCapabilities{
			EnvironmentNone:      proto.CapabilityUnsupported,
			Skills:               proto.CapabilityUnsupported,
			FunctionTools:        proto.CapabilityUnsupported,
			FunctionResultImages: proto.CapabilityUnsupported,
			ToolSearch:           proto.CapabilityUnsupported,
			StdioMCP:             proto.CapabilityUnsupported,
		},
		Executor: i.executor,
	}
	i.loader.AddTo(&view)
	return view
}

func (i viewInstall) executor(ctx context.Context, req proto.PromptRequestPayload, session agent.ViewSession) (agent.Executor, error) {
	return startExecutor(ctx, req, i.node, func(ctx context.Context) (launchOptions, error) {
		return i.prepare(ctx, req, session)
	})
}

// prepare writes the native configuration into the Session home and renders
// a closed environment. The CLI and its worker use the gateway the request
// names and the MCP in session; the request's workspace is the sandbox's.
func (i viewInstall) prepare(_ context.Context, req proto.PromptRequestPayload, session agent.ViewSession) (launchOptions, error) {
	local := req.LocalEnvironment
	if !req.StrictResume || local == nil || req.DisableExecutionEnvironment || req.WorkspaceReadOnly {
		return launchOptions{}, fmt.Errorf("%w: a MiniMax Code view runs Agents API execution in a writable Environment workspace", agent.ErrUnsupportedOperation)
	}
	workspace := local.WorkspaceRoot
	if !path.IsAbs(workspace) || path.Clean(workspace) != workspace || workspace == "/" {
		return launchOptions{}, errors.New("mcode: the workspace is not a canonical absolute path")
	}
	servers, err := workspaceMCP(session.MCP, func(agent.MCPBinding) (map[string]any, error) {
		return nil, fmt.Errorf("%w: a MiniMax Code view does not run stdio MCP", agent.ErrUnsupportedOperation)
	})
	if err != nil {
		return launchOptions{}, err
	}
	private := req
	private.LocalEnvironment, private.DisableExecutionEnvironment, private.MCPHTTPServers = nil, true, nil
	prepared, err := validateOptions(private)
	if err != nil {
		return launchOptions{}, err
	}

	// The Session user owns the home after the first Launch; stay within it.
	home, err := os.OpenRoot(session.Home.Host)
	if err != nil {
		return launchOptions{}, err
	}
	defer home.Close()
	for _, dir := range []string{viewDataName, viewTempName, viewPIName} {
		if err := home.MkdirAll(dir, 0o700); err != nil {
			return launchOptions{}, err
		}
	}
	data, err := home.OpenRoot(viewDataName)
	if err != nil {
		return launchOptions{}, err
	}
	defer data.Close()
	opts := launchOptions{Dir: workspace, DataDir: filepath.Join(session.Home.Host, viewDataName), bindings: session.MCP,
		start: session.Launch, script: i.cli, home: session.Home.Host}
	dataDir, tempDir := path.Join(session.Home.View, viewDataName), path.Join(session.Home.View, viewTempName)
	tools := workspaceTools{node: i.node, bridge: i.bridge, profile: map[string]any{"workspace": workspace, "scratch": tempDir, "network": "enabled"}}
	if err := writeNativeConfig(private, prepared, data, dataDir, &tools); err != nil {
		return opts, err
	}
	opts.Model = prepared.Model
	opts.MCP = append([]map[string]any{tools.server(dataDir)}, servers...)
	opts.Env = []string{
		"PATH=" + path.Join(agent.ViewPrivateRoot, agent.ViewShimName),
		"TMPDIR=" + tempDir,
		"PI_CODING_AGENT_DIR=" + path.Join(session.Home.View, viewPIName),
		"MAVIS_BUILTIN_SKILLS_DIR=" + path.Join(i.assets, "skills"),
		"MAVIS_BUILTIN_AGENTS_DIR=" + path.Join(i.assets, "agents"),
		"MAVIS_BUILTIN_AGENTS_V2_DIR=" + path.Join(i.assets, "agents"),
	}
	if i.loader.LibraryPath != "" {
		opts.Env = append(opts.Env, "LD_LIBRARY_PATH="+i.loader.LibraryPath)
	}
	opts.Env = append(opts.Env, nativeEnvironment(private, dataDir)...)
	opts.spawn = session.Spawn
	opts.reader = clirunner.StartOptions{Binary: i.node, Args: []string{path.Join(path.Dir(i.bridge), "subagent-snapshot.mjs"), dataDir}, Dir: dataDir, Env: opts.Env, OwnProcessGroup: true}
	return opts, nil
}
