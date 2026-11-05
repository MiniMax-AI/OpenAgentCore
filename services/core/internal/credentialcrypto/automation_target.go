package credentialcrypto

import (
	"encoding/json"
	"unicode/utf8"
)

// SealAutomationTarget seals the confidential Session request of an
// Automation target, bound to its tenant and Automation.
func (c *Cipher) SealAutomationTarget(plaintext []byte, tenantID, automationID string) ([]byte, error) {
	aad, err := automationTargetData(tenantID, automationID)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenAutomationTarget(ciphertext []byte, tenantID, automationID string) ([]byte, error) {
	aad, err := automationTargetData(tenantID, automationID)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}

func automationTargetData(tenantID, automationID string) ([]byte, error) {
	for _, value := range []string{tenantID, automationID} {
		if value == "" || !utf8.ValidString(value) {
			return nil, errInvalidBinding
		}
	}
	return json.Marshal(struct {
		Domain       string `json:"domain"`
		Version      byte   `json:"version"`
		TenantID     string `json:"tenant_id"`
		AutomationID string `json:"automation_id"`
	}{"oac.automation-target", formatVersion, tenantID, automationID})
}
