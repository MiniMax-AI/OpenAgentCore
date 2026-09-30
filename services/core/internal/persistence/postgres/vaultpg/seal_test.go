package vaultpg_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// The Store seals to the canonical binding Core has always used, so secrets
// sealed before the Store owned the key still open. A missing key is
// credentialcrypto.ErrUnavailable, and a wrong binding is a decryption
// failure, never a missing key, row or token.
func TestCredentialSecretsKeepTheirSealedFormat(t *testing.T) {
	_, pool := openStore(t)
	cipher := newCipher(t, bytes.Repeat([]byte{61}, 32))
	service, keyless := newService(t, pool, cipher, nil), newService(t, pool, nil, nil)
	tenant, url := uuid.NewString(), "https://mcp.example/tools"
	vault := createVault(t, service, tenant)
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	metadata := vaults.OAuthMetadata{ExpiresAt: &expiry, Refresh: &vaults.OAuthRefreshMetadata{ClientID: "client", TokenEndpoint: "https://issuer.example/token", TokenEndpointAuth: "client_secret_basic"}}
	createOAuth := func(service *vaults.Service, vaultID string) (vaults.Credential, error) {
		return service.CreateOAuthCredential(t.Context(), vaults.CreateOAuthCredential{TenantID: tenant, VaultID: vaultID, Name: "oauth", MCPServerURL: url,
			AccessToken: "new-access", OAuth: metadata, RefreshToken: "refresh"})
	}
	createToken := func(service *vaults.Service, vaultID string) (vaults.Credential, error) {
		return service.CreateStaticCredential(t.Context(), vaults.CreateStaticCredential{TenantID: tenant, VaultID: vaultID, Name: "static", MCPServerURL: url, Token: "new-static"})
	}
	// A non-canonical Vault ID seals to the canonical one.
	static, err := createToken(service, strings.ToUpper(vault.ID))
	if err != nil {
		t.Fatal(err)
	}
	oauth, err := createOAuth(service, strings.ToUpper(vault.ID))
	if err != nil {
		t.Fatal(err)
	}
	scope := func(credential vaults.Credential) credentialcrypto.Binding {
		return credentialcrypto.Binding{TenantID: tenant, VaultID: vault.ID, CredentialID: credential.ID, AuthType: credential.AuthType, Destination: url}
	}
	var ciphertext []byte
	if err := pool.QueryRow(t.Context(), "SELECT token_ciphertext FROM vault_credentials WHERE id=$1", static.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if plaintext, err := cipher.Open(ciphertext, scope(static)); err != nil || string(plaintext) != "new-static" {
		t.Fatal("the token was not sealed to its canonical binding", err)
	}
	if grant := readGrant(t, pool, cipher, tenant, oauth); grant.AccessToken != "new-access" || grant.RefreshToken != "refresh" {
		t.Fatal("the grant was not sealed to its canonical binding")
	}
	// write stores a secret sealed the way Core sealed it before this Store
	// owned the key.
	write := func(credential vaults.Credential, plaintext []byte, binding credentialcrypto.Binding) {
		t.Helper()
		ciphertext, err := cipher.Seal(plaintext, binding)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=$2 WHERE id=$1", credential.ID, ciphertext); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := json.Marshal(storedGrant{Version: 1, Metadata: metadata, AccessToken: "old-access", RefreshToken: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	token := func(service *vaults.Service, credential vaults.Credential) (string, error) {
		return bearerToken(t.Context(), service, tenant, []string{vault.ID}, oauthBinding(credential))
	}
	write(static, []byte("old-static"), scope(static))
	write(oauth, legacy, scope(oauth))
	for _, tc := range []struct {
		credential vaults.Credential
		want       string
	}{{static, "old-static"}, {oauth, "old-access"}} {
		if got, err := token(service, tc.credential); err != nil || got != tc.want {
			t.Fatal("a secret sealed before the move did not open", err)
		}
		if got, err := token(keyless, tc.credential); !errors.Is(err, credentialcrypto.ErrUnavailable) || got != "" {
			t.Fatal("a keyless Store opened a secret", err)
		}
	}
	if _, err := service.UpdateOAuthCredential(t.Context(), vaults.UpdateOAuthCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: oauth.ID, AccessToken: ptr("patched-access")}); err != nil {
		t.Fatal("a grant sealed before the move was not replaced", err)
	}
	if grant := readGrant(t, pool, cipher, tenant, oauth); grant.AccessToken != "patched-access" || grant.RefreshToken != "refresh" {
		t.Fatal("the replaced grant lost its material")
	}
	// Without a key, every write that seals fails first, before a malformed Vault.
	for _, create := range []func(*vaults.Service, string) (vaults.Credential, error){createToken, createOAuth} {
		if _, err := create(keyless, "not-a-vault"); !errors.Is(err, credentialcrypto.ErrUnavailable) {
			t.Fatal("a keyless creation did not fail closed", err)
		}
		if _, err := create(service, "not-a-vault"); !errors.Is(err, vaults.ErrNotFound) {
			t.Fatal("a malformed Vault named one", err)
		}
	}
	if _, err := keyless.UpdateStaticCredential(t.Context(), vaults.UpdateStaticCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: static.ID, Token: "rejected"}); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("a keyless replacement did not fail closed", err)
	}
	if _, err := keyless.UpdateOAuthCredential(t.Context(), vaults.UpdateOAuthCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: oauth.ID, AccessToken: ptr("rejected")}); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("a keyless OAuth replacement did not fail closed", err)
	}
	// A secret sealed to another Credential or destination does not open.
	wrongID, wrongDestination := scope(static), scope(oauth)
	wrongID.CredentialID = oauth.ID
	wrongDestination.Destination += "/other"
	write(static, []byte("old-static"), wrongID)
	write(oauth, legacy, wrongDestination)
	for _, tc := range []struct {
		credential vaults.Credential
		message    string
	}{{static, "MCP credential decryption failed"}, {oauth, "OAuth credential decryption failed"}} {
		if got, err := token(service, tc.credential); err == nil || err.Error() != tc.message || got != "" {
			t.Fatal("a wrong binding was not a decryption failure", err)
		}
	}
}
