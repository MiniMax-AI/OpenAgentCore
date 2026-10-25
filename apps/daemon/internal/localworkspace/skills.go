package localworkspace

import (
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

const CapabilityDirectory = agentcapabilities.Directory

func SkillPath(skill proto.InstalledSkill) string {
	return filepath.Join(skill.InstallationRoot, skill.RelativeRoot)
}
