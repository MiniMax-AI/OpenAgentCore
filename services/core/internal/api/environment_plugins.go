package api

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func decodeEnvironmentPlugins(raw json.RawMessage) ([]store.EnvironmentPlugin, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	result := make([]store.EnvironmentPlugin, 0, len(entries))
	for _, entry := range entries {
		var input struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Source      json.RawMessage `json:"source"`
		}
		if decodeInputObject(entry, &input, "type", "name", "description", "source") != nil || input.Type != "inline" {
			return nil, store.ErrInvalidInput
		}
		body, err := decodeCapabilityArchive(input.Source)
		if err != nil {
			return nil, err
		}
		result = append(result, store.EnvironmentPlugin{Metadata: agentplugin.Metadata{Type: input.Type, Name: input.Name, Description: input.Description}, Archive: body})
	}
	return result, store.ValidateEnvironmentPlugins(result)
}

func pluginResponse(plugins []agentplugin.Metadata) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(plugins))
	for _, plugin := range plugins {
		raw, _ := json.Marshal(plugin)
		result = append(result, raw)
	}
	return result
}

func storedPlugins(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 {
		return []json.RawMessage{}, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		var metadata agentplugin.Metadata
		if decodeInputObject(entry, &metadata, "type", "name", "description") != nil || metadata.Type != "inline" || metadata.Name == "" || metadata.Description == "" || seen[metadata.Name] {
			return nil, store.ErrInvalidInput
		}
		seen[metadata.Name] = true
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}
	return entries, nil
}
