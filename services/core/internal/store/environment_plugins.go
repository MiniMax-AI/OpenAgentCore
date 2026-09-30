package store

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
)

func (s *Store) sealTemplatePlugins(tenant, id string, setup environmentconfig.Setup) ([]byte, []byte, error) {
	metadata, err := json.Marshal(setup.PluginMetadata())
	if err != nil {
		return nil, nil, err
	}
	contents, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "plugins", setup.Plugins, len(setup.Plugins) == 0)
	return metadata, contents, err
}
