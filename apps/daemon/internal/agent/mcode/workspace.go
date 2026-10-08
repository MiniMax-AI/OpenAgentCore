package mcode

import (
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

// workspaceTools is the workspace bridge as the native process runs it, the
// bridge's profile and the installed Skills it presents.
type workspaceTools struct {
	node, bridge string
	profile      map[string]any
	skills       []agentcapabilities.InstalledSkill
}

// server is the bridge's ACP MCP server, with its profile in dataDir as the
// native process sees it.
func (t workspaceTools) server(dataDir string) map[string]any {
	return map[string]any{"name": "oac_workspace", "command": t.node, "args": []string{t.bridge, filepath.Join(dataDir, "workspace-profile.json")}, "env": []map[string]string{}}
}
