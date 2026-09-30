package vaultpg_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/vaultpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// openStore opens the shared test database, as a restarted Core would.
func openStore(t *testing.T) (*vaultpg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Open(t)
	return vaultpg.New(pgunit.NewPool(pool)), pool
}

// readOnlyStore fails every write with a real PostgreSQL error.
func readOnlyStore(t *testing.T, pool *pgxpool.Pool) *vaultpg.Store {
	t.Helper()
	config := pool.Config().Copy()
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	readOnly, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnly.Close)
	return vaultpg.New(pgunit.NewPool(readOnly))
}

// isReadOnlyFailure reports the unexpected failure of a write on a
// readOnlyStore, which the Store returns as is.
func isReadOnlyFailure(err error) bool {
	var failure *pgconn.PgError
	return errors.As(err, &failure) && failure.Code == "25006"
}

func newCipher(t *testing.T, key []byte) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

// newService serves storage; a nil cipher is a Core without a credential key.
func newService(t *testing.T, storage vaults.Storage, cipher *credentialcrypto.Cipher, refresher oauthrefresh.Refresher) *vaults.Service {
	t.Helper()
	if refresher == nil {
		refresher = noRefresh(t)
	}
	service, err := vaults.NewService(storage, cipher, refresher)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type refreshFunc func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error)

func (f refreshFunc) Refresh(ctx context.Context, request oauthrefresh.Request) (oauthrefresh.Token, error) {
	return f(ctx, request)
}

// noRefresh fails the test if an exchange is attempted.
func noRefresh(t *testing.T) refreshFunc {
	return func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		t.Error("unexpected OAuth refresh")
		return oauthrefresh.Token{}, errors.New("unexpected OAuth refresh")
	}
}

func createVault(t *testing.T, service *vaults.Service, tenant string) vaults.Vault {
	t.Helper()
	vault, err := service.CreateVault(t.Context(), vaults.CreateVault{TenantID: tenant})
	if err != nil {
		t.Fatal(err)
	}
	return vault
}

func createStatic(t *testing.T, service *vaults.Service, tenant, vault, name, url, token string) vaults.Credential {
	t.Helper()
	credential, err := service.CreateStaticCredential(t.Context(), vaults.CreateStaticCredential{TenantID: tenant, VaultID: vault, Name: name, MCPServerURL: url, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func resolve(t *testing.T, service *vaults.Service, tenant string, attached []string, requests ...vaults.MCPCredentialRequest) []vaults.MCPCredentialBinding {
	t.Helper()
	bindings, err := service.ResolveMCPCredentials(t.Context(), vaults.ResolveMCPCredentials{TenantID: tenant, VaultIDs: attached, Requests: requests})
	if err != nil || len(bindings) != len(requests) {
		t.Fatal("selection failed", err)
	}
	return bindings
}

func bearerToken(ctx context.Context, service *vaults.Service, tenant string, attached []string, binding vaults.MCPCredentialBinding) (string, error) {
	return service.MCPBearerToken(ctx, vaults.MCPBearerToken{TenantID: tenant, VaultIDs: attached, Binding: binding})
}

func isSelectionError(err error, conflict bool, message string) bool {
	var selection *vaults.MCPCredentialSelectionError
	return errors.As(err, &selection) && selection.Conflict == conflict && selection.Message == message
}

// storedGrant is the sealed plaintext of an mcp_oauth Credential.
type storedGrant struct {
	Version      int                  `json:"version"`
	Metadata     vaults.OAuthMetadata `json:"metadata"`
	AccessToken  string               `json:"access_token"`
	RefreshToken string               `json:"refresh_token"`
	ClientSecret string               `json:"client_secret"`
}

// readGrant opens a stored OAuth grant independently of the service.
func readGrant(t *testing.T, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher, tenant string, credential vaults.Credential) storedGrant {
	t.Helper()
	var ciphertext []byte
	if err := pool.QueryRow(t.Context(), "SELECT token_ciphertext FROM vault_credentials WHERE id=$1", credential.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Open(ciphertext, credentialcrypto.Binding{TenantID: tenant, VaultID: credential.VaultID, CredentialID: credential.ID, AuthType: vaults.AuthMCPOAuth, Destination: credential.MCPServerURL})
	if err != nil {
		t.Fatal("open stored grant", err)
	}
	var grant storedGrant
	if err := json.Unmarshal(plaintext, &grant); err != nil || grant.Version != 1 {
		t.Fatal("decode stored grant", err)
	}
	return grant
}
