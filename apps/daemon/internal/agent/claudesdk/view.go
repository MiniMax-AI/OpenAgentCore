package claudesdk

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/viewloader"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// In an agent-host view, node, the bridge and the SDK's native claude run from
// the closure. Everything else Claude Code runs goes to the sandbox through the
// shims, and its model and MCP traffic goes to the Session's gateway.

// viewLayout holds the view paths the view Executor launches with.
type viewLayout struct {
	node, bridge string
	// libraries is LD_LIBRARY_PATH, empty when the closure binaries are static.
	libraries string
}

// viewHomeDirs are the native directories under the Session home.
var viewHomeDirs = []string{"config", "home", "tmp", "xdg"}

// newView declares the view from the probed install: probe's Node and
// Entrypoint, and the native binary the runtime check reported.
func newView(probe Config, info RuntimeInfo) (*agent.View, error) {
	node, err := filepath.EvalSymlinks(probe.Node)
	if err != nil {
		return nil, err
	}
	entrypoint, err := filepath.EvalSymlinks(probe.Entrypoint)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(filepath.Dir(entrypoint))
	native := filepath.Join(root, info.NativePath)
	if !filepath.IsLocal(info.NativePath) || !executableFile(node) || !executableFile(native) {
		return nil, errors.New("Node or the native Claude Code binary is not an executable file in the bundle")
	}
	if resolved, err := filepath.EvalSymlinks(native); err != nil || resolved != native {
		return nil, errors.New("the native Claude Code path leaves the bundle")
	}
	loader, err := viewloader.For(node, native)
	if err != nil {
		return nil, err
	}
	bridge, err := filepath.Rel(root, entrypoint)
	if err != nil {
		return nil, err
	}
	view := declareView(probe, info, node, root, filepath.ToSlash(bridge), loader)
	if err := view.Validate(); err != nil {
		return nil, err
	}
	return view, nil
}

// declareView declares the view of the install that info describes. The view
// runs the bridge in workspace mode, so its functions follow the probe as a
// workspace Runtime's do.
func declareView(probe Config, info RuntimeInfo, node, root, bridge string, loader viewloader.Fragment) *agent.View {
	native := filepath.ToSlash(info.NativePath)
	nodeMount := agent.ViewMount{Name: "node", HostDir: filepath.Dir(node)}
	bundle := agent.ViewMount{Name: "claude-sdk", HostDir: root}
	layout := viewLayout{node: nodeMount.Path() + "/" + filepath.Base(node), bridge: bundle.Path() + "/" + bridge, libraries: loader.LibraryPath}
	view := &agent.View{
		Closure: []agent.ViewMount{nodeMount, bundle},
		// The managed policy tier would let the sandbox inject settings (C1).
		Masks:     []agent.ViewMask{{Path: "/etc/claude-code", Dir: true}},
		LocalExec: []string{layout.node, bundle.Path() + "/" + native},
		// ps stays local so the kill tree never signals sandbox PIDs (C8).
		Shims:      []string{"bash", "rg", "git"},
		ForwardEnv: []string{"CLAUDECODE", "GIT_EDITOR"},
		Proxy:      agent.ViewProxyEnv,
		Capabilities: agent.ViewCapabilities{
			EnvironmentNone:      proto.CapabilitySupported,
			Skills:               proto.CapabilityUnsupported,
			FunctionTools:        proto.CapabilityFromBool(info.SupportsWorkspaceFunctions()),
			FunctionResultImages: proto.CapabilityFromBool(info.SupportsFunctionResultImages()),
			ToolSearch:           proto.CapabilityFromBool(info.SupportsWorkspaceToolSearch()),
			StdioMCP:             proto.CapabilitySupported,
		},
	}
	loader.AddTo(view)
	view.Executor = newViewExecutorFactory(probe, layout)
	return view
}

func newViewExecutorFactory(probe Config, layout viewLayout) agent.ViewExecutorFactory {
	checked := &runtimeCheckCache{}
	return func(ctx context.Context, req proto.PromptRequestPayload, view agent.ViewSession) (agent.Executor, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := preparationOnly(req); err != nil {
			return nil, err
		}
		start, env, err := prepareView(layout, req, view)
		if err != nil {
			return nil, err
		}
		return startExecutor(ctx, checked, probe, start, func() (*session, error) {
			return startSession(view.Launch, clirunner.StartOptions{Parent: ctx, Binary: layout.node, Args: []string{layout.bridge}, Dir: start.Cwd, Env: env, NeedStdin: true})
		})
	}
}

// prepareView builds the start request for one Session from the request and
// the view: the workspace profile in the sandbox's workspace, or no workspace
// in the work directory with environment none. MCP comes only from the view,
// and the environment is closed.
func prepareView(layout viewLayout, req proto.PromptRequestPayload, view agent.ViewSession) (startRequest, []string, error) {
	environment := req.LocalEnvironment
	if (environment == nil) != req.DisableExecutionEnvironment || environment != nil && !workspacePathSyntax(environment.WorkspaceRoot) || view.Launch == nil || view.Proxy == "" {
		return startRequest{}, nil, errors.New("claudesdk: a view Executor requires the sandbox workspace or environment none, Launch and the gateway proxy")
	}
	// The gateway adds each credential and header, and the Harness runs each
	// stdio alias without arguments.
	servers, _, err := mcpServers(view.MCP, func(stdio proto.EnvironmentMCP) (string, []string) { return stdio.Server.Command, nil })
	if err != nil {
		return startRequest{}, nil, err
	}
	start, provider, err := prepareOptions(req, len(servers) != 0)
	if err != nil {
		return startRequest{}, nil, err
	}
	if err := viewHome(view.Home); err != nil {
		return startRequest{}, nil, err
	}
	profile, env := viewEnvironment(layout, view.Home.View, view.Proxy, provider, environment != nil)
	if environment == nil {
		// Environment none has no Environment MCP, so each server is HTTP.
		start.Cwd = path.Join(view.Home.View, agent.ViewWorkName)
		if len(servers) != 0 {
			http := make([]mcpHTTPServer, 0, len(servers))
			for _, server := range servers {
				http = append(http, server.mcpHTTPServer)
			}
			start.MCPHTTPServers = &http
		}
		return start, env, nil
	}
	profile.NetworkAccess, profile.MCP = environment.NetworkAccess, servers
	start.Workspace, start.Cwd = profile, environment.WorkspaceRoot
	return start, env, nil
}

// viewHome lays out the native directories. A later Executor finds the tree
// the Session uid has owned, so every operation stays inside one os.Root and
// an existing entry must be a directory, not a link.
func viewHome(home agent.ViewDir) error {
	if !workspacePathSyntax(home.Host) || !workspacePathSyntax(home.View) {
		return errors.New("claudesdk: invalid Session home")
	}
	root, err := os.OpenRoot(home.Host)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range viewHomeDirs {
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if info, err := root.Lstat(name); err != nil || !info.IsDir() {
			return fmt.Errorf("claudesdk: Session home entry %s is not a directory", name)
		}
	}
	return nil
}

// viewEnvironment is the complete Harness environment. home is the Session
// home's view path, and workspace adds what the workspace tools need.
func viewEnvironment(layout viewLayout, home, proxy string, provider []string, workspace bool) (*workspaceProfile, []string) {
	shims := agent.ViewPrivateRoot + "/" + agent.ViewShimName
	profile := &workspaceProfile{Home: home + "/home", State: home + "/config", Scratch: home + "/tmp", EnvNames: []string{}, AllowedDomains: []string{}}
	env := []string{
		"PATH=" + shims, "HOME=" + profile.Home, "TMPDIR=" + profile.Scratch, "CLAUDE_CONFIG_DIR=" + profile.State,
		// The messaging socket path stays local and short (C5).
		"XDG_RUNTIME_DIR=" + home + "/xdg",
		"CLAUDE_CODE_CERT_STORE=bundled",         // C2
		"CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1", // C9
		"CLAUDE_CODE_TOOL_MEMORY_LIMIT=0",        // C13
	}
	if workspace {
		env = append(env,
			// Bash runs the sandbox shell through the shim (C3).
			"SHELL="+shims+"/bash", "CLAUDE_CODE_SHELL="+shims+"/bash",
			// The shell's cwd file must be at the same path on both sides
			// (C4). An empty root has no /tmp, where Claude Code creates it.
			"CLAUDE_CODE_TMPDIR=/tmp/oac-claude-"+strings.ToLower(rand.Text()),
			"USE_BUILTIN_RIPGREP=0", // C7: rg runs through its shim
		)
	}
	env = append(env, nativeFlags...)
	if layout.libraries != "" {
		env = append(env, "LD_LIBRARY_PATH="+layout.libraries)
	}
	selected := append(slices.Clone(provider), "HTTPS_PROXY="+proxy, "HTTP_PROXY="+proxy, "NO_PROXY=127.0.0.1,localhost")
	for _, entry := range selected {
		name, _, _ := strings.Cut(entry, "=")
		profile.EnvNames = append(profile.EnvNames, name)
	}
	env = append(env, selected...)
	return profile, append(env, "https_proxy="+proxy, "http_proxy="+proxy, "no_proxy=127.0.0.1,localhost")
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
