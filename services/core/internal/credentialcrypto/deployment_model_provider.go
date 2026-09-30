package credentialcrypto

import (
	"encoding/json"
	"unicode/utf8"
)

// The deployment default model provider is bound to its harness, so one
// harness's ciphertext cannot be read as another's, as an Agent default or as
// a Session snapshot.
func deploymentModelProviderData(harness string) ([]byte, error) {
	if harness == "" || !utf8.ValidString(harness) {
		return nil, errInvalidBinding
	}
	return json.Marshal([]string{"parsar.agents-api.deployment-model-provider.v1", harness})
}

func (c *Cipher) SealDeploymentModelProvider(plaintext []byte, harness string) ([]byte, error) {
	aad, err := deploymentModelProviderData(harness)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenDeploymentModelProvider(ciphertext []byte, harness string) ([]byte, error) {
	aad, err := deploymentModelProviderData(harness)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}
