package environmentconfig

import "github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"

const maxPluginsArchiveBytes = 10 << 20

// Plugin separates safe identity from confidential immutable input.
type Plugin struct {
	Metadata agentplugin.Metadata `json:"metadata"`
	Archive  []byte               `json:"archive"`
}

func ValidatePlugins(plugins []Plugin) error {
	if len(plugins) > 50 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	compressed, expanded := 0, 0
	for _, plugin := range plugins {
		compressed += len(plugin.Archive)
		if compressed > maxPluginsArchiveBytes || seen[plugin.Metadata.Name] {
			return ErrInvalid
		}
		seen[plugin.Metadata.Name] = true
		bundle, err := agentplugin.Read(plugin.Archive, plugin.Metadata)
		if err != nil {
			return ErrInvalid
		}
		for _, file := range bundle.Files {
			expanded += len(file.Data)
		}
		if expanded > 50<<20 {
			return ErrInvalid
		}
	}
	return nil
}

// PluginMetadata returns the public metadata of every Plugin in order.
func (s Setup) PluginMetadata() []agentplugin.Metadata {
	result := make([]agentplugin.Metadata, 0, len(s.Plugins))
	for _, plugin := range s.Plugins {
		result = append(result, plugin.Metadata)
	}
	return result
}
