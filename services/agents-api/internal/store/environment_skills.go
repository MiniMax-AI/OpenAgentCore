package store

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
)

// EnvironmentSkillMetadata is either template intent or concrete installed metadata.
// References acquire their name, description and concrete version at Session commit.
type EnvironmentSkillMetadata struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	SkillID     string `json:"skill_id,omitempty"`
	Version     string `json:"version,omitempty"`
}

// EnvironmentSkill keeps confidential content separate from public metadata.
// A template reference has no archive; a committed Session always has frozen bytes.
type EnvironmentSkill struct {
	Metadata EnvironmentSkillMetadata `json:"metadata"`
	Archive  []byte                   `json:"archive,omitempty"`
}

// InstallationMetadata removes public reference identity at the installer boundary.
func (s EnvironmentSkill) InstallationMetadata() agentskill.Metadata {
	return agentskill.Metadata{Type: "inline", Name: s.Metadata.Name, Description: s.Metadata.Description}
}

const MaxSkillsArchiveBytes = 10 << 20

func ValidateEnvironmentSkills(skills []EnvironmentSkill) error {
	return validateEnvironmentSkills(skills, false)
}

func ValidateInstalledSkillMetadata(metadata EnvironmentSkillMetadata) error {
	if metadata.Name == "" || metadata.Description == "" {
		return ErrInvalidInput
	}
	switch metadata.Type {
	case "inline":
		if metadata.SkillID != "" || metadata.Version != "" {
			return ErrInvalidInput
		}
	case "skill_reference":
		if metadata.SkillID == "" {
			return ErrInvalidInput
		}
		if _, err := skillVersionNumber(metadata.Version); err != nil {
			return err
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func validateEnvironmentSkills(skills []EnvironmentSkill, installed bool) error {
	if len(skills) > 50 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	total := 0
	expanded := 0
	for _, skill := range skills {
		if !installed && skill.Metadata.Type == "skill_reference" {
			m := skill.Metadata
			if m.SkillID == "" || len(m.SkillID) > 256 || m.Name != "" || m.Description != "" || len(skill.Archive) != 0 {
				return ErrInvalidInput
			}
			if m.Version != "" && m.Version != "latest" {
				if _, err := skillVersionNumber(m.Version); err != nil {
					return err
				}
			}
			continue
		}
		if err := ValidateInstalledSkillMetadata(skill.Metadata); err != nil {
			return err
		}
		total += len(skill.Archive)
		if total > MaxSkillsArchiveBytes || seen[skill.Metadata.Name] {
			return ErrInvalidInput
		}
		seen[skill.Metadata.Name] = true
		files, err := agentskill.Read(skill.Archive, skill.InstallationMetadata())
		if err != nil {
			return ErrInvalidInput
		}
		for _, file := range files {
			expanded += len(file.Data)
		}
		if expanded > 50<<20 {
			return ErrInvalidInput
		}
	}
	return nil
}

func (s EnvironmentSetup) SkillMetadata() []EnvironmentSkillMetadata {
	result := make([]EnvironmentSkillMetadata, 0, len(s.Skills))
	for _, skill := range s.Skills {
		result = append(result, skill.Metadata)
	}
	return result
}

func (s *Store) sealTemplateSkills(tenant, id string, setup EnvironmentSetup) ([]byte, []byte, error) {
	metadata, err := json.Marshal(setup.SkillMetadata())
	if err != nil {
		return nil, nil, err
	}
	contents, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "skills", setup.Skills, len(setup.Skills) == 0)
	return metadata, contents, err
}
