package main

import (
	"encoding/base64"
	"os"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
)

func credentialCipher() (*credentialcrypto.Cipher, error) {
	path := os.Getenv("OAC_CREDENTIAL_KEY_FILE")
	if path == "" {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, configurationFailure("OAC_CREDENTIAL_KEY_FILE", "cannot read credential key file", err)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(content)))
	if err != nil || len(key) != 32 {
		return nil, configurationFailure("OAC_CREDENTIAL_KEY_FILE", "must contain a base64-encoded random 32-byte key", nil)
	}
	return credentialcrypto.New(key)
}
