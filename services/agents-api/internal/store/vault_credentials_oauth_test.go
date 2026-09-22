package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/oauthrefresh"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type oauthRefreshFunc func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error)

func (f oauthRefreshFunc) Refresh(ctx context.Context, request oauthrefresh.Request) (oauthrefresh.Token, error) {
	return f(ctx, request)
}
func oauthString(value string) *string { return &value }

func oauthFixture(t *testing.T, refresher oauthrefresh.Refresher) (*Store, *pgxpool.Pool, string, Vault, CreateOAuthCredentialInput) {
	t.Helper()
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipherAndOAuthRefresh(pool, cipher, refresher)
	tenant := uuid.NewString()
	vault, err := s.CreateVault(t.Context(), tenant, CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateOAuthCredentialInput{Name: "OAuth fixture", MCPServerURL: "https://mcp.example/tools",
		AccessToken: "private-access-canary", RefreshToken: "private-refresh-canary", ClientSecret: "private-client-canary",
		OAuth: OAuthMetadata{ExpiresAt: oauthString(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)),
			Refresh: &OAuthRefreshMetadata{ClientID: "test-client", TokenEndpoint: "https://issuer.example/token",
				TokenEndpointAuth: "client_secret_basic", Resource: oauthString("https://mcp.example/tools"), Scope: oauthString("read write")}}}
	return s, pool, tenant, vault, input
}

func createOAuthFixture(t *testing.T, s *Store, tenant string, vault Vault, input CreateOAuthCredentialInput) Credential {
	t.Helper()
	credential, err := s.CreateOAuthCredential(t.Context(), tenant, vault.ID, input)
	if err != nil {
		t.Fatal("create OAuth fixture", err)
	}
	return credential
}

func oauthFixtureBinding(credential Credential) MCPCredentialBinding {
	return MCPCredentialBinding{ServerLabel: "test", ServerURL: credential.MCPServerURL,
		VaultID: credential.VaultID, CredentialID: credential.ID, AuthType: credential.AuthType}
}

func storedOAuthSecret(t *testing.T, s *Store, tenant string, credential Credential) oauthSecret {
	t.Helper()
	tx, _, secret, err := s.lockOAuth(t.Context(), tenant, credential.VaultID, credential.ID, "")
	if err != nil {
		t.Fatal("read private test grant", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestOAuthCredentialMetadataEncryptionAndScope(t *testing.T) {
	var exchanges atomic.Int32
	s, pool, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		exchanges.Add(1)
		return oauthrefresh.Token{}, errors.New("unexpected exchange")
	}))
	credential := createOAuthFixture(t, s, tenant, vault, input)
	for _, reader := range []*Store{New(pool), s} {
		got, err := reader.GetCredential(t.Context(), tenant, vault.ID, credential.ID)
		if err != nil || !reflect.DeepEqual(got, credential) {
			t.Fatal("keyless safe metadata changed", err)
		}
		page, err := reader.ListCredentials(t.Context(), tenant, vault.ID, "", 20, true, nil)
		if err != nil || len(page.Credentials) != 1 || !reflect.DeepEqual(page.Credentials[0], credential) {
			t.Fatal("keyless listing failed", err)
		}
		encoded, _ := json.Marshal(page)
		for _, secret := range []string{input.AccessToken, input.RefreshToken, input.ClientSecret} {
			if bytes.Contains(encoded, []byte(secret)) {
				t.Fatal("metadata disclosed a secret")
			}
		}
	}
	var ciphertext []byte
	if err := pool.QueryRow(t.Context(), "SELECT token_ciphertext FROM vault_credentials WHERE id=$1", credential.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{input.AccessToken, input.RefreshToken, input.ClientSecret} {
		if bytes.Contains(ciphertext, []byte(secret)) {
			t.Fatal("plaintext grant persisted")
		}
	}
	restored := NewWithCredentialCipherAndOAuthRefresh(pool, s.credentialCipher, s.oauthRefresher)
	if secret := storedOAuthSecret(t, restored, tenant, credential); secret.AccessToken != input.AccessToken || secret.RefreshToken != input.RefreshToken || secret.ClientSecret != input.ClientSecret {
		t.Fatal("restart lost grant material")
	}
	binding := oauthFixtureBinding(credential)
	for _, target := range []struct {
		tenant  string
		vaults  []string
		binding MCPCredentialBinding
	}{
		{uuid.NewString(), []string{vault.ID}, binding}, {tenant, nil, binding},
		{tenant, []string{vault.ID}, MCPCredentialBinding{ServerLabel: "test", ServerURL: input.MCPServerURL + "/other", VaultID: vault.ID, CredentialID: credential.ID, AuthType: "mcp_oauth"}},
	} {
		if token, err := s.MCPBearerToken(t.Context(), target.tenant, target.vaults, target.binding); !errors.Is(err, ErrNotFound) || token != "" {
			t.Fatal("foreign or mismatched scope admitted", err)
		}
	}
	if _, err := s.UpdateOAuthCredential(t.Context(), uuid.NewString(), vault.ID, credential.ID, UpdateOAuthCredentialInput{AccessToken: oauthString("replacement")}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign update admitted")
	}
	if _, err := s.CreateOAuthCredential(t.Context(), uuid.NewString(), vault.ID, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign creation admitted")
	}
	if _, err := New(pool).CreateOAuthCredential(t.Context(), tenant, vault.ID, input); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("keyless creation admitted")
	}
	if token, err := New(pool).MCPBearerToken(t.Context(), tenant, []string{vault.ID}, binding); !errors.Is(err, ErrCredentialStorageUnavailable) || token != "" {
		t.Fatal("keyless execution admitted")
	}
	wrongCipher, _ := credentialcrypto.New(bytes.Repeat([]byte{18}, 32))
	wrong := NewWithCredentialCipherAndOAuthRefresh(pool, wrongCipher, s.oauthRefresher)
	if token, err := wrong.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, binding); err == nil || token != "" {
		t.Fatal("wrong key executed")
	}
	if exchanges.Load() != 0 {
		t.Fatal("resource operations or invalid scopes contacted provider")
	}
}

func TestOAuthCredentialUpdatesPreservePinnedSemantics(t *testing.T) {
	s, _, tenant, vault, input := oauthFixture(t, nil)
	credential := createOAuthFixture(t, s, tenant, vault, input)
	update := func(patch UpdateOAuthCredentialInput) Credential {
		t.Helper()
		got, err := s.UpdateOAuthCredential(t.Context(), tenant, vault.ID, credential.ID, patch)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != credential.ID || got.Name != credential.Name || got.AuthType != credential.AuthType || got.MCPServerURL != credential.MCPServerURL || !got.CreatedAt.Equal(credential.CreatedAt) {
			t.Fatal("immutable metadata changed")
		}
		return got
	}
	unchanged := update(UpdateOAuthCredentialInput{})
	if !reflect.DeepEqual(unchanged.OAuth, credential.OAuth) {
		t.Fatal("omitted values changed")
	}
	replacement := update(UpdateOAuthCredentialInput{AccessToken: oauthString("new-access")})
	if replacement.OAuth.ExpiresAt != nil {
		t.Fatal("new access token retained old expiry")
	}
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	replacement = update(UpdateOAuthCredentialInput{ExpiresAtSet: true, ExpiresAt: &expiry, Refresh: &OAuthRefreshUpdate{ScopeSet: true, Scope: nil}})
	if replacement.OAuth.ExpiresAt == nil || *replacement.OAuth.ExpiresAt != expiry || replacement.OAuth.Refresh.Scope != nil {
		t.Fatal("expiry or null scope semantics failed")
	}
	secret := storedOAuthSecret(t, s, tenant, replacement)
	if secret.AccessToken != "new-access" || secret.RefreshToken != input.RefreshToken || secret.ClientSecret != input.ClientSecret {
		t.Fatal("omitted secrets were replaced")
	}
	replacement = update(UpdateOAuthCredentialInput{ExpiresAtSet: true, Refresh: &OAuthRefreshUpdate{RefreshToken: oauthString("new-refresh"), TokenEndpointAuthType: "client_secret_basic", ClientSecret: oauthString("new-secret"), ScopeSet: true, Scope: oauthString("read")}})
	secret = storedOAuthSecret(t, s, tenant, replacement)
	if secret.Metadata.ExpiresAt != nil || secret.RefreshToken != "new-refresh" || secret.ClientSecret != "new-secret" || *secret.Metadata.Refresh.Scope != "read" {
		t.Fatal("replacement fields not persisted")
	}
	for _, patch := range []UpdateOAuthCredentialInput{
		{ExpiresAtSet: true, ExpiresAt: oauthString("not-a-date")},
		{Refresh: &OAuthRefreshUpdate{TokenEndpointAuthType: "client_secret_post"}},
	} {
		if _, err := s.UpdateOAuthCredential(t.Context(), tenant, vault.ID, credential.ID, patch); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid mutation admitted")
		}
	}
	if got := storedOAuthSecret(t, s, tenant, replacement); !reflect.DeepEqual(got, secret) {
		t.Fatal("rejected update changed the grant")
	}
	input.OAuth.Refresh = nil
	input.RefreshToken = ""
	input.ClientSecret = ""
	noRefresh := createOAuthFixture(t, s, tenant, vault, input)
	if _, err := s.UpdateOAuthCredential(t.Context(), tenant, vault.ID, noRefresh.ID, UpdateOAuthCredentialInput{Refresh: &OAuthRefreshUpdate{RefreshToken: oauthString("cannot-add")}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("added missing refresh configuration")
	}
}

func TestOAuthMetadataTamperingNeverReachesProvider(t *testing.T) {
	var exchanges atomic.Int32
	s, pool, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		exchanges.Add(1)
		return oauthrefresh.Token{}, errors.New("unexpected refresh")
	}))
	for _, mutation := range []string{
		`jsonb_set(oauth_metadata,'{refresh,token_endpoint}','"https://attacker.example/token"')`,
		`jsonb_set(oauth_metadata,'{refresh,client_id}','"other-client"')`,
		`jsonb_set(oauth_metadata,'{refresh,scope}','"all"')`,
		`jsonb_set(oauth_metadata,'{expires_at}','null')`,
	} {
		credential := createOAuthFixture(t, s, tenant, vault, input)
		if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET oauth_metadata="+mutation+" WHERE id=$1", credential.ID); err != nil {
			t.Fatal(err)
		}
		if token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential)); err == nil || token != "" {
			t.Fatal("metadata substitution executed")
		}
		if _, err := s.UpdateOAuthCredential(t.Context(), tenant, vault.ID, credential.ID, UpdateOAuthCredentialInput{AccessToken: oauthString("new")}); err == nil {
			t.Fatal("update authenticated substituted metadata")
		}
	}
	if exchanges.Load() != 0 {
		t.Fatal("tampered metadata reached token endpoint")
	}
}

func TestOAuthCredentialSelectionIncludesBothAuthTypes(t *testing.T) {
	s, _, tenant, vault, input := oauthFixture(t, nil)
	credential := createOAuthFixture(t, s, tenant, vault, input)
	requests := []MCPCredentialRequest{{ServerLabel: "test", ServerURL: input.MCPServerURL}}
	bindings, err := s.ResolveMCPCredentials(t.Context(), tenant, []string{vault.ID}, requests)
	if err != nil || len(bindings) != 1 || bindings[0].AuthType != "mcp_oauth" || bindings[0].CredentialID != credential.ID {
		t.Fatal("OAuth was not selected", err)
	}
	if _, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, CreateStaticCredentialInput{Name: "Static", MCPServerURL: input.MCPServerURL, Token: "static"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveMCPCredentials(t.Context(), tenant, []string{vault.ID}, requests); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("ambiguous mixed credentials selected")
	}
	requests[0].CredentialID = &credential.ID
	if selected, err := s.ResolveMCPCredentials(t.Context(), tenant, []string{vault.ID}, requests); err != nil || selected[0] != bindings[0] {
		t.Fatal("explicit OAuth identity changed")
	}
}

func TestOAuthRefreshErrorsAreSafeAndPreserveGrant(t *testing.T) {
	for _, mode := range []string{"provider_error", "empty_access", "expired_response", "no_refresh"} {
		t.Run(mode, func(t *testing.T) {
			s, _, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
				switch mode {
				case "provider_error":
					return oauthrefresh.Token{}, errors.New("private-refresh-canary: provider body")
				case "expired_response":
					past := time.Now().Add(-time.Second)
					return oauthrefresh.Token{AccessToken: "new", ExpiresAt: &past}, nil
				}
				return oauthrefresh.Token{}, nil
			}))
			if mode == "no_refresh" {
				input.OAuth.Refresh = nil
				input.RefreshToken = ""
				input.ClientSecret = ""
			}
			credential := createOAuthFixture(t, s, tenant, vault, input)
			before := storedOAuthSecret(t, s, tenant, credential)
			token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential))
			if err == nil || token != "" || strings.Contains(err.Error(), "private-refresh-canary") {
				t.Fatal("refresh failed unsafely")
			}
			if after := storedOAuthSecret(t, s, tenant, credential); !reflect.DeepEqual(before, after) {
				t.Fatal("failed refresh changed grant")
			}
		})
	}
}
