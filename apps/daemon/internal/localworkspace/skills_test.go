package localworkspace

import (
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestSkillPathUsesRuntimeInstallationRoot(t *testing.T) {
	root := t.TempDir()
	skill := proto.InstalledSkill{InstallationRoot: root, RelativeRoot: "plugins/0/skills/proof"}
	if got := SkillPath(skill); got != filepath.Join(root, "plugins/0/skills/proof") {
		t.Fatal("Runtime root lost", got)
	}
}
