package codex

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

const (
	// viewClosureName presents the install directory at ViewPrivateRoot/codex.
	viewClosureName = "codex"
	// codeModeHostName is the helper codex runs from beside its own binary.
	codeModeHostName = "codex-code-mode-host"
	// viewCodexHome and viewTempDir are CODEX_HOME and TMPDIR under the
	// Session home, as siblings: codex refuses to create its helper aliases
	// in a CODEX_HOME under TMPDIR.
	viewCodexHome = "codex"
	viewTempDir   = "tmp"
)

// viewForwardEnv lists what codex sets for the commands it runs: the unified
// exec defaults, its thread and session identity, and GIT_OPTIONAL_LOCKS for
// its own git calls. LANG is the view's.
var viewForwardEnv = []string{"NO_COLOR", "TERM", "LC_CTYPE", "LC_ALL", "COLORTERM", "PAGER", "GIT_PAGER", "GH_PAGER", "CODEX_CI", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "GIT_OPTIONAL_LOCKS"}

// viewDisabledFeatures run local programs or load code the view does not
// declare: shell snapshots source rc files through the shim, hooks spawn a
// local shell, plugin sync and memories run git and npm against CODEX_HOME,
// and the skill MCP dependency install opens a browser.
var viewDisabledFeatures = []string{"shell_snapshot", "hooks", "plugins", "memories", "skill_mcp_dependency_install"}

// viewLaunch is the agent-host view a Codex Executor runs in.
type viewLaunch struct {
	agent.ViewSession
	// binary is the LocalExec path of codex.
	binary string
}

// discoverView declares the view for the install discovery found. Only the
// pinned release is qualified, and only a static binary runs from its
// closure alone.
func discoverView(version string) *agent.View {
	if !SupportsNativeSessionRecovery(version) {
		return nil
	}
	binary, err := exec.LookPath(defaultBinary())
	if err == nil {
		binary, err = filepath.Abs(binary)
	}
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err != nil || !staticELF(binary) {
		return nil
	}
	view := newView(binary, staticELF(filepath.Join(filepath.Dir(binary), codeModeHostName)))
	return &view
}

// newView presents binary's directory as the closure. Commands codex runs
// through the passwd shell and git run in the sandbox; rg stays undeclared
// so codex falls back to its own search.
func newView(binary string, codeModeHost bool) agent.View {
	mount := agent.ViewMount{Name: viewClosureName, HostDir: filepath.Dir(binary)}
	launch := mount.Path() + "/" + filepath.Base(binary)
	local := []string{launch}
	if codeModeHost {
		local = append(local, mount.Path()+"/"+codeModeHostName)
	}
	return agent.View{
		Closure:    []agent.ViewMount{mount},
		Masks:      []agent.ViewMask{{Path: "/etc/codex", Dir: true}},
		LocalExec:  local,
		Shims:      []string{"git"},
		ShimPaths:  []string{"/bin/bash"},
		ForwardEnv: slices.Clone(viewForwardEnv),
		Proxy:      agent.ViewProxyEnv,
		Capabilities: agent.ViewCapabilities{
			EnvironmentNone:      proto.CapabilitySupported,
			Skills:               proto.CapabilityUnsupported,
			FunctionTools:        proto.CapabilitySupported,
			FunctionResultImages: proto.CapabilitySupported,
			ToolSearch:           proto.CapabilityUnsupported,
			StdioMCP:             proto.CapabilitySupported,
		},
		Executor: func(ctx context.Context, req proto.PromptRequestPayload, session agent.ViewSession) (agent.Executor, error) {
			cfg := defaultSessionConfig()
			cfg.codexBinary = binary
			cfg.view = &viewLaunch{ViewSession: session, binary: launch}
			executor, err := newExecutor(ctx, req, cfg)
			if executor == nil {
				return nil, err
			}
			return executor, err
		},
	}
}

// staticELF reports whether name is a regular executable ELF file that needs
// no interpreter and no shared library.
func staticELF(name string) bool {
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return false
	}
	file, err := elf.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	for _, prog := range file.Progs {
		if prog.Type == elf.PT_INTERP {
			return false
		}
	}
	libraries, err := file.ImportedLibraries()
	return err == nil && len(libraries) == 0
}

// prepareViewPlan builds the plan for codex in the view: the Environment's
// workspace as cwd, or the work directory with environment none, CODEX_HOME
// and TMPDIR in the Session home, MCP only from the Session, and a closed
// environment.
func prepareViewPlan(ctx context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (SessionPlan, error) {
	view := cfg.view
	if !filepath.IsAbs(view.Home.Host) || !path.IsAbs(view.Home.View) {
		return SessionPlan{}, errors.New("codex: view home must be absolute")
	}
	cwd := path.Join(view.Home.View, agent.ViewWorkName)
	if local := req.LocalEnvironment; local != nil {
		cwd = local.WorkspaceRoot
	}
	if (req.LocalEnvironment == nil) != req.DisableExecutionEnvironment || !path.IsAbs(cwd) {
		return SessionPlan{}, fmt.Errorf("%w: codex: a view runs in an Environment workspace or with environment none", agent.ErrUnsupportedOperation)
	}
	if _, err := runtimePermissionProfile(req); err != nil {
		return SessionPlan{}, err
	}
	// The Harness runs each stdio alias without arguments, which the native
	// configuration reports as an empty list.
	servers, _, err := mcpServersFromBindings(view.MCP, func(stdio proto.EnvironmentMCP) (string, []string) { return stdio.Server.Command, []string{} })
	if err != nil {
		return SessionPlan{}, err
	}
	plan, err := buildSessionPlan(req, func() (agent.ViewDir, error) { return viewHome(view.Home) })
	if err != nil {
		return SessionPlan{}, fmt.Errorf("codex: build session plan: %w", err)
	}
	if err := configureSubagentObservations(&plan, req); err != nil {
		plan.Cleanup()
		return SessionPlan{}, err
	}
	disableProgrammaticTools(&plan, req.ExecutionControls)
	plan.Cwd = cwd
	plan.Sandbox = SandboxDangerFullAcces
	plan.Permissions = ""
	plan.ApprovalPolicy = AskForApproval{String: "never"}
	if req.DisableSubagents {
		disableSubagents(&plan)
	}
	if err := configureMCP(&plan, servers); err != nil {
		plan.Cleanup()
		return SessionPlan{}, err
	}
	for _, feature := range viewDisabledFeatures {
		plan.EnableFeatures = slices.DeleteFunc(plan.EnableFeatures, func(value string) bool { return value == feature })
		if !slices.Contains(plan.DisableFeatures, feature) {
			plan.DisableFeatures = append(plan.DisableFeatures, feature)
		}
		if override := [2]string{"features." + feature, "false"}; !slices.Contains(plan.ExtraConfig, override) {
			plan.ExtraConfig = append(plan.ExtraConfig, override)
		}
	}
	// No login shell, and no ancestor walk above the workspace over the
	// mount. No trust entry is written, so the project stays untrusted.
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"allow_login_shell", "false"}, [2]string{"project_root_markers", "[]"})
	if req.ExecutionControls != nil {
		if err := viewModelVerbosity(ctx, cfg.codexBinary, &plan, req.ModelProvider); err != nil {
			plan.Cleanup()
			return SessionPlan{}, err
		}
	}
	env := []string{
		"HOME=" + view.Home.View,
		"PATH=" + agent.ViewPrivateRoot + "/" + agent.ViewShimName,
		"TMPDIR=" + path.Join(view.Home.View, viewTempDir),
	}
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
		env = append(env, name+"="+view.Proxy)
	}
	plan.Env = append(append(env, "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost"), plan.Env...)
	return plan, nil
}

// viewHome lays out CODEX_HOME and TMPDIR in the Session home. The Session
// user owns the home after a Launch, so neither may be a link.
func viewHome(home agent.ViewDir) (agent.ViewDir, error) {
	root, err := os.OpenRoot(home.Host)
	if err != nil {
		return agent.ViewDir{}, fmt.Errorf("codex: open view home: %w", err)
	}
	defer root.Close()
	for _, name := range []string{viewCodexHome, viewTempDir} {
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return agent.ViewDir{}, fmt.Errorf("codex: create view home %s: %w", name, err)
		}
		if info, err := root.Lstat(name); err != nil || !info.IsDir() {
			return agent.ViewDir{}, fmt.Errorf("codex: view home %s is not a directory", name)
		}
	}
	return agent.ViewDir{Host: filepath.Join(home.Host, viewCodexHome), View: path.Join(home.View, viewCodexHome)}, nil
}

// viewModelVerbosity reads the catalog from the trusted install on this host,
// with a scratch CODEX_HOME that holds only the Session's provider.
func viewModelVerbosity(ctx context.Context, binary string, plan *SessionPlan, provider *modelprovider.Provider) error {
	scratch, err := os.MkdirTemp("", "oac-codex-catalog-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	home, tmp := filepath.Join(scratch, "home"), filepath.Join(scratch, "tmp")
	for _, dir := range []string{home, tmp} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
	}
	if provider != nil {
		if err := writeCodexProviderConfig(home, nativeProvider(*provider)); err != nil {
			return err
		}
	}
	probe := catalogProbe{binary: binary, dir: home, env: []string{"HOME=" + home, "CODEX_HOME=" + home, "TMPDIR=" + tmp, "DISABLE_TELEMETRY=1"}}
	return verifyModelVerbosity(ctx, probe, plan)
}
