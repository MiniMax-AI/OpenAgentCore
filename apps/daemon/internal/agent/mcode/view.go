package mcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// In an agent-host view, node runs the CLI from the closure and the native
// data directory lives in the Session home. The workspace worker runs beside
// the CLI in the same view; bash, rg and git run in the sandbox.

// Closure names and Session home directories.
const (
	viewNodeMount    = "node"
	viewHarnessMount = "mcode-harness"
	viewLibraryMount = "node-lib"
	viewDataName     = "data"
	viewTempName     = "tmp"
	viewPIName       = "pi-agent"
)

// viewInstall is the trusted MiniMax Code install as a view presents it.
type viewInstall struct {
	closure  []agent.ViewMount
	overlays []agent.ViewOverlay
	masks    []agent.ViewMask
	// libraryPath is LD_LIBRARY_PATH in the view; empty for a static node.
	libraryPath string
	// node, cli, bridge and assets are view paths: assets holds the CLI's
	// builtin skills and agents.
	node, cli, bridge, assets string
	reader                    historyReader
}

func discoverView(parent context.Context, options agent.DiscoveryOptions) *agent.View {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	install, err := findViewInstall(ctx)
	if err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: mcode agent-host view unavailable: %v\n", err)
		return nil
	}
	view := install.view()
	return &view
}

func findViewInstall(ctx context.Context) (viewInstall, error) {
	if runtime.GOOS != "linux" {
		return viewInstall{}, errors.New("agent-host views run on Linux")
	}
	programs, err := findPrograms()
	if err != nil {
		return viewInstall{}, err
	}
	node, err := filepath.EvalSymlinks(programs.node)
	if err != nil {
		return viewInstall{}, err
	}
	loader, err := findLoader(ctx, node) // TODO(viewloader)
	if err != nil {
		return viewInstall{}, err
	}
	return newViewInstall(node, programs.binary, programs.bridge, loader)
}

// newViewInstall presents node's directory, the harness directory holding the
// bridge and the CLI, and node's library directories as the closure. node is
// a resolved host path.
func newViewInstall(node, cli, bridge string, loader nodeLoader) (viewInstall, error) {
	var i viewInstall
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
	i.reader = historyReader{node: node, script: filepath.Join(harness, "subagent-snapshot.mjs")}
	if info, err := os.Lstat(i.reader.script); err != nil || !info.Mode().IsRegular() {
		return i, errors.New("the Subagent history reader is missing")
	}

	// TODO(viewloader): take the loader's overlay, closure, masks and
	// LD_LIBRARY_PATH from the shared view loader.
	if loader.interp == "" {
		return i, nil
	}
	i.overlays = []agent.ViewOverlay{{Path: loader.interp, Source: loader.source, Exec: true}}
	// The loader reads both from the sandbox's /etc otherwise.
	i.masks = []agent.ViewMask{{Path: "/etc/ld.so.cache"}, {Path: "/etc/ld.so.preload"}}
	mounts := map[string]string{nodeMount.HostDir: nodeMount.Path(), harnessMount.HostDir: harnessMount.Path()}
	var libraryPath []string
	for _, dir := range loader.libraries {
		mount, ok := mounts[dir]
		if !ok {
			name := viewLibraryMount
			if n := len(i.closure) - 1; n > 1 {
				name += "-" + strconv.Itoa(n)
			}
			m := agent.ViewMount{Name: name, HostDir: dir}
			i.closure = append(i.closure, m)
			mount, mounts[dir] = m.Path(), m.Path()
		}
		if !slices.Contains(libraryPath, mount) {
			libraryPath = append(libraryPath, mount)
		}
	}
	i.libraryPath = strings.Join(libraryPath, ":")
	return i, nil
}

func (i viewInstall) view() agent.View {
	masks := append([]agent.ViewMask{
		// The CLI probes these for builtin assets and its install receipt
		// before its own copy.
		{Path: "/assets", Dir: true}, {Path: "/opt/assets", Dir: true}, {Path: "/install.json"}, {Path: "/opt/install.json"},
		// Node resolves a package the closure lacks, such as the worker's
		// optional ripgrep, from /node_modules.
		{Path: "/node_modules", Dir: true},
	}, i.masks...)
	// No forwarded tool needs a Harness variable, so ForwardEnv is empty.
	return agent.View{
		Closure:   slices.Clone(i.closure),
		Overlays:  slices.Clone(i.overlays),
		Masks:     masks,
		LocalExec: []string{i.node},
		Shims:     []string{"git", "rg"},
		ShimPaths: []string{"/bin/bash"},
		Proxy:     agent.ViewProxyNone,
		Executor:  i.executor,
	}
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
	if local.NetworkAccess != "enabled" || len(local.AllowedDomains) != 0 {
		return launchOptions{}, fmt.Errorf("%w: a MiniMax Code view requires unrestricted workspace network", agent.ErrUnsupportedOperation)
	}
	if len(local.Skills) != 0 {
		return launchOptions{}, fmt.Errorf("%w: a MiniMax Code view does not install Capabilities", agent.ErrUnsupportedOperation)
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
	if err := validateOptions(private); err != nil {
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
	if opts.Model, err = writeNativeConfig(private, data); err != nil {
		return opts, err
	}
	dataDir, tempDir := path.Join(session.Home.View, viewDataName), path.Join(session.Home.View, viewTempName)
	opts.Env = []string{
		"PATH=" + path.Join(agent.ViewPrivateRoot, agent.ViewShimName),
		"TMPDIR=" + tempDir,
		"PI_CODING_AGENT_DIR=" + path.Join(session.Home.View, viewPIName),
		"MAVIS_BUILTIN_SKILLS_DIR=" + path.Join(i.assets, "skills"),
		"MAVIS_BUILTIN_AGENTS_DIR=" + path.Join(i.assets, "agents"),
		"MAVIS_BUILTIN_AGENTS_V2_DIR=" + path.Join(i.assets, "agents"),
	}
	if i.libraryPath != "" {
		opts.Env = append(opts.Env, "LD_LIBRARY_PATH="+i.libraryPath)
	}
	opts.Env = append(opts.Env, nativeEnvironment(private, dataDir)...)
	if !req.DisableSubagents {
		reader := i.reader
		reader.home = session.Home.Host
		opts.reader = &reader
	}
	profile := map[string]any{"workspace": workspace, "scratch": tempDir, "network": "enabled"}
	tools := workspaceTools{node: i.node, bridge: i.bridge, profile: path.Join(dataDir, "workspace-profile.json")}
	return opts, writeWorkspaceTools(&opts, data, req, tools, profile, nil, servers)
}
