package credentialcrypto

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
)

// SkillBinding prevents encrypted bundles from being moved across owners or versions.
type SkillBinding struct {
	TenantID  string `json:"tenant_id"`
	SkillID   string `json:"skill_id"`
	VersionID string `json:"version_id"`
	Version   string `json:"version"`
}

// NewSkillBinding binds a version's archive to its tenant, Skill, version ID
// and version number.
func NewSkillBinding(tenant, skill, versionID uuid.UUID, version int64) SkillBinding {
	return SkillBinding{TenantID: tenant.String(), SkillID: skill.String(), VersionID: versionID.String(), Version: strconv.FormatInt(version, 10)}
}

func (c *Cipher) SealSkill(body []byte, binding SkillBinding) ([]byte, error) {
	aad, err := skillData(binding)
	if err != nil {
		return nil, err
	}
	return c.seal(body, aad)
}

func (c *Cipher) OpenSkill(body []byte, binding SkillBinding) ([]byte, error) {
	aad, err := skillData(binding)
	if err != nil {
		return nil, err
	}
	return c.open(body, aad)
}

func skillData(binding SkillBinding) ([]byte, error) {
	for _, value := range []string{binding.TenantID, binding.SkillID, binding.VersionID, binding.Version} {
		if value == "" || !utf8.ValidString(value) {
			return nil, errInvalidBinding
		}
	}
	return json.Marshal(struct {
		Domain  string       `json:"domain"`
		Version byte         `json:"version"`
		Binding SkillBinding `json:"binding"`
	}{"parsar.agents-api.skill", formatVersion, binding})
}
