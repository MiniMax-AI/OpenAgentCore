package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
)

// WorkspaceConfig binds one trusted private placement. It does not create an
// isolation boundary or authorize a public Environment. The operator must place
// the entire factory inside the qualified outer mount/process boundary first.
// State directories must already exist.
// PublicDirectory may name a second mount of the same workspace inode.
type WorkspaceConfig struct {
	Directory       string
	PublicDirectory string
	NetworkAccess   string
	AllowedDomains  []string
	HomeDir         string
	ScratchDir      string
}

type workspaceProfile struct {
	ToolEnv        map[string]string                  `json:"tool_env,omitempty"`
	CapabilityRoot string                             `json:"capability_root,omitempty"`
	MCP            []environmentMCPServer             `json:"mcp,omitempty"`
	Skills         []agentcapabilities.InstalledSkill `json:"skills,omitempty"`
	Home           string                             `json:"home"`
	State          string                             `json:"state"`
	Scratch        string                             `json:"scratch"`
	EnvNames       []string                           `json:"env_names"`
	NetworkAccess  string                             `json:"network_access,omitempty"`
	AllowedDomains []string                           `json:"allowed_domains,omitempty"`
}

func prepareWorkspace(config Config, req proto.PromptRequestPayload) (*workspaceProfile, []string, error) {
	if req.DisableExecutionEnvironment || req.MCPHTTPServers != nil {
		return nil, nil, fmt.Errorf("claudesdk: workspace profile does not support the requested execution combination")
	}
	if req.WorkDir != "" && req.WorkDir != config.Workspace.Directory {
		return nil, nil, fmt.Errorf("claudesdk: work_dir conflicts with the trusted workspace binding")
	}
	if req.LocalEnvironment != nil && !(agentnetwork.Policy{Access: config.Workspace.NetworkAccess, AllowedDomains: config.Workspace.AllowedDomains}).Equal(agentnetwork.Policy{Access: req.LocalEnvironment.NetworkAccess, AllowedDomains: req.LocalEnvironment.AllowedDomains}) {
		return nil, nil, fmt.Errorf("claudesdk: local Runtime network policy mismatch")
	}
	profile, env, err := workspaceEnvironment(config)
	if err != nil {
		return nil, nil, err
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

func workspaceCwd(w *WorkspaceConfig) string {
	if w.PublicDirectory != "" {
		return w.PublicDirectory
	}
	return w.Directory
}

// Harness state paths and selected model credentials overlay the user environment.
func workspaceEnvironment(config Config) (*workspaceProfile, []string, error) {
	fail := func() (*workspaceProfile, []string, error) {
		return nil, nil, fmt.Errorf("claudesdk: invalid trusted workspace configuration")
	}
	w := config.Workspace
	if w == nil || (w.NetworkAccess != "" && w.NetworkAccess != "enabled") || len(w.AllowedDomains) != 0 {
		return fail()
	}
	for _, path := range []string{config.Node, config.Entrypoint, filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js")} {
		info, err := os.Stat(path)
		if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() {
			return fail()
		}
	}
	for _, path := range []string{workspaceCwd(w), config.StateDir, w.HomeDir, w.ScratchDir} {
		if !canonicalWorkspaceDir(path) {
			return fail()
		}
	}
	if w.PublicDirectory != "" {
		actual, err := os.Stat(w.Directory)
		alias, aliasErr := os.Stat(w.PublicDirectory)
		if err != nil || aliasErr != nil || !os.SameFile(actual, alias) {
			return fail()
		}
	}
	profile := &workspaceProfile{Home: w.HomeDir, State: config.StateDir, Scratch: w.ScratchDir, EnvNames: []string{}, NetworkAccess: w.NetworkAccess, AllowedDomains: []string{}}
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "ANTHROPIC_API_KEY" && name != "ANTHROPIC_AUTH_TOKEN" && name != "CLAUDE_CODE_OAUTH_TOKEN" && name != "CLAUDE_CONFIG_DIR" && name != "HOME" && name != "TMPDIR" {
			env = append(env, entry)
		}
	}
	env = append(env, "HOME="+w.HomeDir, "TMPDIR="+w.ScratchDir, "CLAUDE_CONFIG_DIR="+config.StateDir, "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1")
	seen := map[string]bool{}
	for _, entry := range config.Env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || seen[name] || strings.ContainsRune(entry, '\x00') || !workspaceEnvName(name) {
			return fail()
		}
		seen[name] = true
		profile.EnvNames = append(profile.EnvNames, name)
		env = append(env, entry)
	}
	return profile, env, nil
}

func workspaceEnvName(name string) bool {
	switch name {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
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
