package credentialcrypto

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

func sandboxDeploymentData(installation string, generation uint64) ([]byte, error) {
	if installation == "" || !utf8.ValidString(installation) || generation == 0 {
		return nil, errInvalidBinding
	}
	return json.Marshal([]string{"parsar.agents-api.sandbox-deployment.v1", installation, strconv.FormatUint(generation, 10)})
}

func (c *Cipher) SealSandboxDeployment(plaintext []byte, installation string, generation uint64) ([]byte, error) {
	aad, err := sandboxDeploymentData(installation, generation)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenSandboxDeployment(ciphertext []byte, installation string, generation uint64) ([]byte, error) {
	aad, err := sandboxDeploymentData(installation, generation)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}
