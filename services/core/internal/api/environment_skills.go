package api

import (
	"bytes"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func decodeEnvironmentSkills(raw json.RawMessage) ([]store.EnvironmentSkill, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	result := make([]store.EnvironmentSkill, 0, len(entries))
	for _, entry := range entries {
		var discriminator struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(entry, &discriminator) != nil {
			return nil, store.ErrInvalidInput
		}
		if discriminator.Type == "skill_reference" {
			var reference struct {
				Type    string          `json:"type"`
				SkillID string          `json:"skill_id"`
				Version json.RawMessage `json:"version"`
			}
			if decodeInputObject(entry, &reference, "type", "skill_id", "version") != nil {
				return nil, store.ErrInvalidInput
			}
			metadata := store.EnvironmentSkillMetadata{Type: reference.Type, SkillID: reference.SkillID}
			if len(reference.Version) > 0 && !bytes.Equal(bytes.TrimSpace(reference.Version), []byte("null")) {
				if json.Unmarshal(reference.Version, &metadata.Version) != nil || metadata.Version == "" {
					return nil, store.ErrInvalidInput
				}
			}
			result = append(result, store.EnvironmentSkill{Metadata: metadata})
			continue
		}
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
		result = append(result, store.EnvironmentSkill{Metadata: store.EnvironmentSkillMetadata{Type: input.Type, Name: input.Name, Description: input.Description}, Archive: body})
	}
	return result, store.ValidateEnvironmentSkills(result)
}

func skillResponse(skills []store.EnvironmentSkillMetadata) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(skills))
	for _, skill := range skills {
		var projection any = skill
		if skill.Type == "skill_reference" && skill.Version == "" {
			projection = struct {
				store.EnvironmentSkillMetadata
				Version *string `json:"version"`
			}{EnvironmentSkillMetadata: skill}
		}
		raw, _ := json.Marshal(projection)
		result = append(result, raw)
	}
	return result
}

func storedSkills(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 {
		return []json.RawMessage{}, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		var metadata store.EnvironmentSkillMetadata
		if decodeInputObject(entry, &metadata, "type", "name", "description", "skill_id", "version") != nil || store.ValidateInstalledSkillMetadata(metadata) != nil || seen[metadata.Name] {
			return nil, store.ErrInvalidInput
		}
		seen[metadata.Name] = true
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}
	return entries, nil
}
