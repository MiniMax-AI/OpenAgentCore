package store

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
)

func (s *Store) sealTemplateSkills(tenant, id string, setup environmentconfig.Setup) ([]byte, []byte, error) {
	metadata, err := json.Marshal(setup.SkillMetadata())
	if err != nil {
		return nil, nil, err
	}
	contents, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "skills", setup.Skills, len(setup.Skills) == 0)
	return metadata, contents, err
}
