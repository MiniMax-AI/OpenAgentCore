package vaultpg

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// binding scopes a Credential secret to its canonical tenant, Vault and
// Credential IDs, its authentication type and its destination. Every stored
// ciphertext is sealed to exactly these fields.
func binding(tenant, vault, id pgtype.UUID, authType, destination string) credentialcrypto.Binding {
	return credentialcrypto.Binding{TenantID: uuid.UUID(tenant.Bytes).String(), VaultID: uuid.UUID(vault.Bytes).String(),
		CredentialID: uuid.UUID(id.Bytes).String(), AuthType: authType, Destination: destination}
}

// oauthPayload is the sealed plaintext of an mcp_oauth Credential. Its sealed
// copy of the metadata authenticates the stored one.
type oauthPayload struct {
	Version      int                  `json:"version"`
	Metadata     vaults.OAuthMetadata `json:"metadata"`
	AccessToken  string               `json:"access_token"`
	RefreshToken string               `json:"refresh_token"`
	ClientSecret string               `json:"client_secret"`
}

// sealStatic seals a static_bearer token.
func (s *Store) sealStatic(scope credentialcrypto.Binding, token string) ([]byte, error) {
	if s.cipher == nil {
		return nil, credentialcrypto.ErrUnavailable
	}
	ciphertext, err := s.cipher.Seal([]byte(token), scope)
	if err != nil {
		return nil, errors.New("credential encryption failed")
	}
	return ciphertext, nil
}

// openStatic opens a static_bearer token.
func (s *Store) openStatic(scope credentialcrypto.Binding, ciphertext []byte) (string, error) {
	if s.cipher == nil {
		return "", credentialcrypto.ErrUnavailable
	}
	plaintext, err := s.cipher.Open(ciphertext, scope)
	if err != nil {
		return "", errors.New("MCP credential decryption failed")
	}
	return string(plaintext), nil
}

// sealOAuth encodes the grant's metadata for its column and seals the whole
// grant.
func (s *Store) sealOAuth(scope credentialcrypto.Binding, grant vaults.OAuthGrant) (metadata, ciphertext []byte, err error) {
	if s.cipher == nil {
		return nil, nil, credentialcrypto.ErrUnavailable
	}
	metadata, err = json.Marshal(grant.Metadata)
	if err != nil {
		return nil, nil, errors.New("credential encoding failed")
	}
	plaintext, err := json.Marshal(oauthPayload{Version: 1, Metadata: grant.Metadata, AccessToken: grant.AccessToken,
		RefreshToken: grant.RefreshToken, ClientSecret: grant.ClientSecret})
	if err != nil {
		return nil, nil, errors.New("credential encoding failed")
	}
	ciphertext, err = s.cipher.Seal(plaintext, scope)
	if err != nil {
		return nil, nil, errors.New("credential encryption failed")
	}
	return metadata, ciphertext, nil
}

// openOAuth opens a grant and authenticates the stored metadata against the
// sealed copy.
func (s *Store) openOAuth(scope credentialcrypto.Binding, stored *vaults.OAuthMetadata, ciphertext []byte) (vaults.OAuthGrant, error) {
	if s.cipher == nil {
		return vaults.OAuthGrant{}, credentialcrypto.ErrUnavailable
	}
	plaintext, err := s.cipher.Open(ciphertext, scope)
	if err != nil {
		return vaults.OAuthGrant{}, errors.New("OAuth credential decryption failed")
	}
	var payload oauthPayload
	if json.Unmarshal(plaintext, &payload) != nil || payload.Version != 1 || stored == nil || !reflect.DeepEqual(payload.Metadata, *stored) {
		return vaults.OAuthGrant{}, errors.New("OAuth credential authentication failed")
	}
	return vaults.OAuthGrant{Metadata: payload.Metadata, AccessToken: payload.AccessToken,
		RefreshToken: payload.RefreshToken, ClientSecret: payload.ClientSecret}, nil
}
