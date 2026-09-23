package credentialcrypto

import (
	"encoding/json"
	"unicode/utf8"
)

func agentModelExecutionData(tenantID, agentID string) ([]byte, error) {
	if tenantID == "" || agentID == "" || !utf8.ValidString(tenantID) || !utf8.ValidString(agentID) {
		return nil, errInvalidBinding
	}
	return json.Marshal([]string{"parsar.agents-api.agent-model-execution.v1", tenantID, agentID})
}

func (c *Cipher) SealAgentModelExecution(plaintext []byte, tenantID, agentID string) ([]byte, error) {
	aad, err := agentModelExecutionData(tenantID, agentID)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenAgentModelExecution(ciphertext []byte, tenantID, agentID string) ([]byte, error) {
	aad, err := agentModelExecutionData(tenantID, agentID)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}
