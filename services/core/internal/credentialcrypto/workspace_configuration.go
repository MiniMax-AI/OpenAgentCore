package credentialcrypto

import (
	"encoding/json"
	"unicode/utf8"
)

// A workspace filesystem credential is bound to its installation and to the
// immutable configuration that owns it, so it cannot be read as another
// configuration's or another installation's credential.
func workspaceConfigurationData(installation, configuration string) ([]byte, error) {
	if installation == "" || configuration == "" || !utf8.ValidString(installation) || !utf8.ValidString(configuration) {
		return nil, errInvalidBinding
	}
	return json.Marshal([]string{"oac.workspace-configuration.v1", installation, configuration})
}

func (c *Cipher) SealWorkspaceConfiguration(plaintext []byte, installation, configuration string) ([]byte, error) {
	aad, err := workspaceConfigurationData(installation, configuration)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenWorkspaceConfiguration(ciphertext []byte, installation, configuration string) ([]byte, error) {
	aad, err := workspaceConfigurationData(installation, configuration)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}
