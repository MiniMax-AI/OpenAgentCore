package localworkspace

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"path/filepath"
	"testing"
)

func TestSkillPathUsesRuntimeInstallationRoot(t *testing.T) {
	root := t.TempDir()
	skill := agentcapabilities.InstalledSkill{InstallationRoot: root, RelativeRoot: "plugins/0/skills/proof"}
	if got := SkillPath(skill); got != filepath.Join(root, "plugins/0/skills/proof") {
		t.Fatal("Runtime root lost", got)
	}
}
