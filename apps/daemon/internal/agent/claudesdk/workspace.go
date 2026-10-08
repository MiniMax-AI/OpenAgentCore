package claudesdk

import (
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

// workspaceProfile is the bridge's workspace execution profile: the native
// directories, the names of the environment entries the Harness keeps, and
// the Environment's installed MCP and Skills.
type workspaceProfile struct {
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

// nativeFlags turn off Claude Code's telemetry, error reports, updates and
// background work.
var nativeFlags = []string{"DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1"}

func workspacePathSyntax(dir string) bool {
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir && strings.IndexFunc(dir, func(r rune) bool { return r < 32 || r == 127 }) == -1
}
