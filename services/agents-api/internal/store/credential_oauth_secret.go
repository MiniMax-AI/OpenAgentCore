package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
)

// The encrypted copy authenticates every public setting used for refresh, including
// the token endpoint. Substituting database metadata must never redirect a grant.
type oauthSecret struct {
	Version      int           `json:"version"`
	Metadata     OAuthMetadata `json:"metadata"`
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	ClientSecret string        `json:"client_secret"`
}

func validOAuthMetadata(metadata OAuthMetadata) bool {
	if metadata.ExpiresAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, *metadata.ExpiresAt); err != nil {
			return false
		}
	}
	if refresh := metadata.Refresh; refresh != nil {
		if refresh.ClientID == "" || refresh.TokenEndpoint == "" {
			return false
		}
		switch refresh.TokenEndpointAuth {
		case "none", "client_secret_basic", "client_secret_post":
		default:
			return false
		}
	}
	return true
}

func oauthBinding(tenantID string, credential Credential) credentialcrypto.Binding {
	return credentialcrypto.Binding{TenantID: tenantID, VaultID: credential.VaultID,
		CredentialID: credential.ID, AuthType: "mcp_oauth", Destination: credential.MCPServerURL}
}

func (s *Store) sealOAuth(tenantID string, credential Credential, secret oauthSecret) ([]byte, []byte, error) {
	if s.credentialCipher == nil {
		return nil, nil, ErrCredentialStorageUnavailable
	}
	if !validOAuthMetadata(secret.Metadata) {
		return nil, nil, ErrInvalidInput
	}
	metadata, err := json.Marshal(secret.Metadata)
	if err != nil {
		return nil, nil, errors.New("credential encoding failed")
	}
	plaintext, err := json.Marshal(secret)
	if err != nil {
		return nil, nil, errors.New("credential encoding failed")
	}
	ciphertext, err := s.credentialCipher.Seal(plaintext, oauthBinding(tenantID, credential))
	if err != nil {
		return nil, nil, errors.New("credential encryption failed")
	}
	return metadata, ciphertext, nil
}

func (s *Store) openOAuth(tenantID string, credential Credential, ciphertext []byte) (oauthSecret, error) {
	if s.credentialCipher == nil {
		return oauthSecret{}, ErrCredentialStorageUnavailable
	}
	plaintext, err := s.credentialCipher.Open(ciphertext, oauthBinding(tenantID, credential))
	if err != nil {
		return oauthSecret{}, errors.New("OAuth credential decryption failed")
	}
	var secret oauthSecret
	if json.Unmarshal(plaintext, &secret) != nil || secret.Version != 1 || credential.OAuth == nil ||
		!reflect.DeepEqual(secret.Metadata, *credential.OAuth) || !validOAuthMetadata(secret.Metadata) {
		return oauthSecret{}, errors.New("OAuth credential authentication failed")
	}
	return secret, nil
}
