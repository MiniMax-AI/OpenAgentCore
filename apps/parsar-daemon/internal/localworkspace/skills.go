package localworkspace

import (
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
)

const CapabilityDirectory = agentcapabilities.Directory

// LoadSkills consumes only the packaged installation after binding authorization.
func LoadSkills() ([]agentcapabilities.InstalledSkill, error) {
	actual, err := filepath.EvalSymlinks(CapabilityDirectory)
	if err != nil || actual != CapabilityDirectory {
		return nil, agentcapabilities.ErrInvalid
	}
	root, err := os.OpenRoot(CapabilityDirectory)
	if err != nil {
		return nil, agentcapabilities.ErrInvalid
	}
	defer root.Close()
	manifest, err := agentcapabilities.Load(root)
	if err != nil {
		return nil, err
	}
	return append([]agentcapabilities.InstalledSkill{}, manifest.Skills...), nil
}

func SkillPath(skill agentcapabilities.InstalledSkill) string {
	return filepath.Join(CapabilityDirectory, skill.RelativeRoot)
}
