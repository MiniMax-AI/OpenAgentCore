package localworkspace

import (
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
)

const CapabilityDirectory = agentcapabilities.Directory

// LoadCapabilities consumes the packaged installation after binding authorization.
func LoadCapabilities() (agentcapabilities.Manifest, error) {
	actual, err := filepath.EvalSymlinks(CapabilityDirectory)
	if err != nil || actual != CapabilityDirectory {
		return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
	}
	root, err := os.OpenRoot(CapabilityDirectory)
	if err != nil {
		return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
	}
	defer root.Close()
	return agentcapabilities.Load(root)
}

func SkillPath(skill agentcapabilities.InstalledSkill) string {
	return filepath.Join(CapabilityDirectory, skill.RelativeRoot)
}
