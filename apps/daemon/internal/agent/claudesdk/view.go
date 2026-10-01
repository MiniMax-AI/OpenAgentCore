package claudesdk

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	view := declareView(probe, node, root, filepath.ToSlash(bridge), filepath.ToSlash(info.NativePath), loader)
	if err := view.Validate(); err != nil {
		return nil, err
	}
	return view, nil
}

func declareView(probe Config, node, root, bridge, native string, loader viewloader.Fragment) *agent.View {
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
			return startSession(view.Launch, clirunner.StartOptions{Parent: ctx, Binary: layout.node, Args: []string{layout.bridge}, Dir: start.Cwd, Env: env, NeedStdin: true, OwnProcessGroup: true})
		})
	}
}

// prepareView builds the workspace profile for one Session from the request
// and the view: the workspace is the sandbox's, MCP comes only from the view,
// and the environment is closed.
func prepareView(layout viewLayout, req proto.PromptRequestPayload, view agent.ViewSession) (startRequest, []string, error) {
	environment := req.LocalEnvironment
	if environment == nil || !workspacePathSyntax(environment.WorkspaceRoot) || req.DisableExecutionEnvironment || view.Launch == nil || view.Proxy == "" {
		return startRequest{}, nil, errors.New("claudesdk: a view Executor requires the sandbox workspace, Launch and the gateway proxy")
	}
	if environment.Capabilities || len(environment.Skills) != 0 || environment.CapabilityRoot != "" {
		return startRequest{}, nil, fmt.Errorf("%w: installed Capabilities in an agent-host view", agent.ErrUnsupportedOperation)
	}
	if (environment.NetworkAccess != "" && environment.NetworkAccess != "enabled") || len(environment.AllowedDomains) != 0 {
		return startRequest{}, nil, fmt.Errorf("%w: restricted network in an agent-host view", agent.ErrUnsupportedOperation)
	}
	servers, err := viewMCP(view.MCP)
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
	profile, env := viewEnvironment(layout, view.Home.View, view.Proxy, provider)
	profile.NetworkAccess, profile.MCP = environment.NetworkAccess, servers
	start.Workspace, start.Cwd = profile, environment.WorkspaceRoot
	return start, env, nil
}

// viewMCP renders the gateway's HTTP endpoints. The gateway adds each
// credential and header, so none is rendered here.
func viewMCP(bindings []agent.MCPBinding) ([]environmentMCPServer, error) {
	declarations := make([]proto.MCPHTTPServer, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Transport != "http" || binding.Stdio != nil {
			return nil, fmt.Errorf("%w: %s MCP in an agent-host view", agent.ErrUnsupportedOperation, binding.Transport)
		}
		declarations = append(declarations, proto.MCPHTTPServer{ServerLabel: binding.ServerLabel, ServerURL: binding.ServerURL, AllowedTools: binding.AllowedTools, Required: binding.Required})
	}
	if err := validateMCPServers(declarations); err != nil {
		return nil, err
	}
	projected, _ := prepareMCPHTTP(&declarations)
	servers := make([]environmentMCPServer, 0, len(*projected))
	for _, server := range *projected {
		servers = append(servers, environmentMCPServer{mcpHTTPServer: server})
	}
	return servers, nil
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
// home's view path.
func viewEnvironment(layout viewLayout, home, proxy string, provider []string) (*workspaceProfile, []string) {
	shims := agent.ViewPrivateRoot + "/" + agent.ViewShimName
	profile := &workspaceProfile{Home: home + "/home", State: home + "/config", Scratch: home + "/tmp", EnvNames: []string{}, AllowedDomains: []string{}}
	env := []string{
		"PATH=" + shims, "HOME=" + profile.Home, "TMPDIR=" + profile.Scratch, "CLAUDE_CONFIG_DIR=" + profile.State,
		// The messaging socket path stays local and short (C5).
		"XDG_RUNTIME_DIR=" + home + "/xdg",
		// Bash runs the sandbox shell through the shim (C3).
		"SHELL=" + shims + "/bash", "CLAUDE_CODE_SHELL=" + shims + "/bash",
		// The shell's cwd file must be at the same path on both sides (C4).
		"CLAUDE_CODE_TMPDIR=/tmp/oac-claude-" + strings.ToLower(rand.Text()),
		"CLAUDE_CODE_CERT_STORE=bundled",         // C2
		"USE_BUILTIN_RIPGREP=0",                  // C7: rg runs through its shim
		"CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1", // C9
		"CLAUDE_CODE_TOOL_MEMORY_LIMIT=0",        // C13
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
