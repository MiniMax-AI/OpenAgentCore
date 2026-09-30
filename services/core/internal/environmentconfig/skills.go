package environmentconfig

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
)

// SkillMetadata is either template intent or concrete installed metadata.
// References acquire their name, description and concrete version at Session commit.
type SkillMetadata struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	SkillID     string `json:"skill_id,omitempty"`
	Version     string `json:"version,omitempty"`
}

// Skill keeps confidential content separate from public metadata.
// A template reference has no archive; a committed Session always has frozen bytes.
type Skill struct {
	Metadata SkillMetadata `json:"metadata"`
	Archive  []byte        `json:"archive,omitempty"`
}

// InstallationMetadata removes public reference identity at the installer boundary.
func (s Skill) InstallationMetadata() agentskill.Metadata {
	return agentskill.Metadata{Type: "inline", Name: s.Metadata.Name, Description: s.Metadata.Description}
}

const maxSkillsArchiveBytes = 10 << 20

// ValidateSkills checks requested Skills, where a reference names a Skill and
// an optional version or "latest".
func ValidateSkills(skills []Skill) error {
	return validateSkills(skills, false)
}

// ValidateInstalled checks the metadata of an installed Skill: an inline Skill
// or a reference resolved to a concrete version.
func (m SkillMetadata) ValidateInstalled() error {
	if m.Name == "" || m.Description == "" {
		return ErrInvalid
	}
	switch m.Type {
	case "inline":
		if m.SkillID != "" || m.Version != "" {
			return ErrInvalid
		}
	case "skill_reference":
		if m.SkillID == "" {
			return ErrInvalid
		}
		if _, err := skills.ParseVersion(m.Version); err != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validateSkills(requested []Skill, installed bool) error {
	if len(requested) > 50 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	total := 0
	expanded := 0
	for _, skill := range requested {
		if !installed && skill.Metadata.Type == "skill_reference" {
			m := skill.Metadata
			if m.SkillID == "" || len(m.SkillID) > 256 || m.Name != "" || m.Description != "" || len(skill.Archive) != 0 {
				return ErrInvalid
			}
			if skills.ValidateSelector(m.Version) != nil {
				return ErrInvalid
			}
			continue
		}
		if err := skill.Metadata.ValidateInstalled(); err != nil {
			return err
		}
		total += len(skill.Archive)
		if total > maxSkillsArchiveBytes || seen[skill.Metadata.Name] {
			return ErrInvalid
		}
		seen[skill.Metadata.Name] = true
		files, err := agentskill.Read(skill.Archive, skill.InstallationMetadata())
		if err != nil {
			return ErrInvalid
		}
		for _, file := range files {
			expanded += len(file.Data)
		}
		if expanded > 50<<20 {
			return ErrInvalid
		}
	}
	return nil
}

// SkillMetadata returns the public metadata of every Skill in order.
func (s Setup) SkillMetadata() []SkillMetadata {
	result := make([]SkillMetadata, 0, len(s.Skills))
	for _, skill := range s.Skills {
		result = append(result, skill.Metadata)
	}
	return result
}
