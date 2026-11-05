package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
)

type workspaceProfile struct {
	ToolEnv        map[string]string      `json:"tool_env,omitempty"`
	CapabilityRoot string                 `json:"capability_root,omitempty"`
	MCP            []environmentMCPServer `json:"mcp,omitempty"`
	Skills         []proto.InstalledSkill `json:"skills,omitempty"`
	Home           string                 `json:"home"`
	State          string                 `json:"state"`
	Scratch        string                 `json:"scratch"`
	EnvNames       []string               `json:"env_names"`
}

func prepareWorkspace(config Config, req proto.PromptRequestPayload) (*workspaceProfile, []string, error) {
	if req.DisableExecutionEnvironment {
		return nil, nil, fmt.Errorf("claudesdk: workspace profile does not support the requested execution combination")
	}
	if req.LocalEnvironment != nil && req.LocalEnvironment.WorkspaceRoot != config.Workspace {
		return nil, nil, fmt.Errorf("claudesdk: workspace root conflicts with the trusted workspace binding")
	}
	// Retained history lives with the Environment filesystem; HOME and scratch stay on the Runtime's own disk.
	kind := harnessconfiguration.Declaration.Kind
	state, err := agent.StateDir(config.StateRoot, kind, req.AgentStateKey)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := agent.StateDir(config.RuntimeRoot, kind, req.AgentStateKey)
	if err != nil {
		return nil, nil, err
	}
	dirs := workspaceDirs{State: state, Home: filepath.Join(runtime, "home"), Scratch: filepath.Join(runtime, "scratch")}
	profile, env, err := workspaceEnvironment(config, dirs)
	if err != nil {
		return nil, nil, err
	}
	for _, dir := range []string{dirs.State, dirs.Home, dirs.Scratch} {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, err
		}
	}
	if req.LocalEnvironment != nil {
		profile.ToolEnv, err = localworkspace.ReadOptionalToolEnvironment()
		if err != nil {
			return nil, nil, err
		}
	}

	if req.LocalEnvironment != nil {
		profile.Skills = req.LocalEnvironment.Skills
		profile.CapabilityRoot = req.LocalEnvironment.CapabilityRoot
	}
	servers, credentials, err := prepareRuntimeMCP(req)
	if err != nil {
		return nil, nil, err
	}
	profile.MCP = servers
	return profile, append(env, credentials...), nil
}

type workspaceDirs struct{ State, Home, Scratch string }

// Harness state paths and selected model credentials overlay the user environment.
func workspaceEnvironment(config Config, dirs workspaceDirs) (*workspaceProfile, []string, error) {
	env, names, err := trustedEnvironment(config)
	if err != nil {
		return nil, nil, err
	}
	env = append(env, "HOME="+dirs.Home, "TMPDIR="+dirs.Scratch, "CLAUDE_CONFIG_DIR="+dirs.State)
	return &workspaceProfile{Home: dirs.Home, State: dirs.State, Scratch: dirs.Scratch, EnvNames: names}, env, nil
}

// trustedEnvironment validates the trusted binding and returns the user environment without Harness state paths or credentials.
func trustedEnvironment(config Config) ([]string, []string, error) {
	fail := func() ([]string, []string, error) {
		return nil, nil, fmt.Errorf("claudesdk: invalid trusted workspace configuration")
	}
	for _, path := range []string{config.Node, config.Entrypoint, filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js")} {
		info, err := os.Stat(path)
		if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() {
			return fail()
		}
	}
	if !canonicalWorkspaceDir(config.Workspace) {
		return fail()
	}
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains([]string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_MAX_OUTPUT_TOKENS", "CLAUDE_CONFIG_DIR", "HOME", "TMPDIR"}, name) {
			env = append(env, entry)
		}
	}
	names := []string{}
	seen := map[string]bool{}
	for _, entry := range config.Env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || seen[name] || strings.ContainsRune(entry, '\x00') || !workspaceEnvName(name) {
			return fail()
		}
		seen[name] = true
		names = append(names, name)
		env = append(env, entry)
	}
	return env, names, nil
}

func workspaceEnvName(name string) bool {
	switch name {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_MAX_OUTPUT_TOKENS",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
		return true
	default:
		return false
	}
}

func canonicalWorkspaceDir(dir string) bool {
	if !workspacePathSyntax(dir) {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
func workspacePathSyntax(dir string) bool {
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir && strings.IndexFunc(dir, func(r rune) bool { return r < 32 || r == 127 }) == -1
}
