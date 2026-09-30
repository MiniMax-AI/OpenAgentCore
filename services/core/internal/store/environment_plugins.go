package store

import (
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

const MaxPluginsArchiveBytes = 10 << 20

// EnvironmentPlugin separates safe identity from confidential immutable input.
type EnvironmentPlugin struct {
	Metadata agentplugin.Metadata `json:"metadata"`
	Archive  []byte               `json:"archive"`
}

func ValidateEnvironmentPlugins(plugins []EnvironmentPlugin) error {
	if len(plugins) > 50 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	compressed, expanded := 0, 0
	for _, plugin := range plugins {
		compressed += len(plugin.Archive)
		if compressed > MaxPluginsArchiveBytes || seen[plugin.Metadata.Name] {
			return ErrInvalidInput
		}
		seen[plugin.Metadata.Name] = true
		bundle, err := agentplugin.Read(plugin.Archive, plugin.Metadata)
		if err != nil {
			return ErrInvalidInput
		}
		for _, file := range bundle.Files {
			expanded += len(file.Data)
		}
		if expanded > 50<<20 {
			return ErrInvalidInput
		}
	}
	return nil
}

func (s EnvironmentSetup) PluginMetadata() []agentplugin.Metadata {
	result := make([]agentplugin.Metadata, 0, len(s.Plugins))
	for _, plugin := range s.Plugins {
		result = append(result, plugin.Metadata)
	}
	return result
}

func (s *Store) sealTemplatePlugins(tenant, id string, setup EnvironmentSetup) ([]byte, []byte, error) {
	metadata, err := json.Marshal(setup.PluginMetadata())
	if err != nil {
		return nil, nil, err
	}
	contents, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "plugins", setup.Plugins, len(setup.Plugins) == 0)
	return metadata, contents, err
}
