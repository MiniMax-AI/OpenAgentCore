package localworkspace

import (
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

const CapabilityDirectory = agentcapabilities.Directory

func SkillPath(skill agentcapabilities.InstalledSkill) string {
	return filepath.Join(skill.InstallationRoot, skill.RelativeRoot)
}
