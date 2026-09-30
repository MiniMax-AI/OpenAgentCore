package vaults

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
)

// fakeStorage fails the test on any call whose func the test did not set.
type fakeStorage struct {
	t                    testing.TB
	getVault             func(context.Context, string, string) (Vault, error)
	listVaults           func(context.Context, string, PageQuery) (VaultPage, error)
	getCredential        func(context.Context, string, string, string) (Credential, error)
	listCredentials      func(context.Context, string, string, PageQuery) (CredentialPage, error)
	createVault          func(context.Context, NewVault) (Vault, error)
	deleteVault          func(context.Context, string, string) (string, error)
	createCredential     func(context.Context, NewCredential) (Credential, error)
	replaceStaticToken   func(context.Context, StaticTokenReplacement) (Credential, error)
	deleteCredential     func(context.Context, CredentialKey) (string, error)
	withOAuthCredential  func(context.Context, CredentialKey, func(OAuthTx) error) error
	countOwnedVaults     func(context.Context, string, []string) (int, error)
	findMCPCredentials   func(context.Context, MCPCredentialQuery) ([]MCPCredentialMatch, error)
	staticTokenCiphertxt func(context.Context, StaticTokenQuery) ([]byte, error)
}

func (f *fakeStorage) unexpected(method string) {
	f.t.Helper()
	f.t.Fatalf("unexpected call to %s", method)
}

func (f *fakeStorage) GetVault(ctx context.Context, tenantID, vaultID string) (Vault, error) {
	if f.getVault == nil {
		f.unexpected("GetVault")
	}
	return f.getVault(ctx, tenantID, vaultID)
}

func (f *fakeStorage) ListVaults(ctx context.Context, tenantID string, query PageQuery) (VaultPage, error) {
	if f.listVaults == nil {
		f.unexpected("ListVaults")
	}
	return f.listVaults(ctx, tenantID, query)
}

func (f *fakeStorage) GetCredential(ctx context.Context, tenantID, vaultID, credentialID string) (Credential, error) {
	if f.getCredential == nil {
		f.unexpected("GetCredential")
	}
	return f.getCredential(ctx, tenantID, vaultID, credentialID)
}

func (f *fakeStorage) ListCredentials(ctx context.Context, tenantID, vaultID string, query PageQuery) (CredentialPage, error) {
	if f.listCredentials == nil {
		f.unexpected("ListCredentials")
	}
	return f.listCredentials(ctx, tenantID, vaultID, query)
}

func (f *fakeStorage) CreateVault(ctx context.Context, vault NewVault) (Vault, error) {
	if f.createVault == nil {
		f.unexpected("CreateVault")
	}
	return f.createVault(ctx, vault)
}

func (f *fakeStorage) DeleteVault(ctx context.Context, tenantID, vaultID string) (string, error) {
	if f.deleteVault == nil {
		f.unexpected("DeleteVault")
	}
	return f.deleteVault(ctx, tenantID, vaultID)
}

func (f *fakeStorage) CreateCredential(ctx context.Context, credential NewCredential) (Credential, error) {
	if f.createCredential == nil {
		f.unexpected("CreateCredential")
	}
	return f.createCredential(ctx, credential)
}

func (f *fakeStorage) ReplaceStaticToken(ctx context.Context, replacement StaticTokenReplacement) (Credential, error) {
	if f.replaceStaticToken == nil {
		f.unexpected("ReplaceStaticToken")
	}
	return f.replaceStaticToken(ctx, replacement)
}

func (f *fakeStorage) DeleteCredential(ctx context.Context, key CredentialKey) (string, error) {
	if f.deleteCredential == nil {
		f.unexpected("DeleteCredential")
	}
	return f.deleteCredential(ctx, key)
}

func (f *fakeStorage) WithOAuthCredential(ctx context.Context, key CredentialKey, apply func(OAuthTx) error) error {
	if f.withOAuthCredential == nil {
		f.unexpected("WithOAuthCredential")
	}
	return f.withOAuthCredential(ctx, key, apply)
}

func (f *fakeStorage) CountOwnedVaults(ctx context.Context, tenantID string, vaultIDs []string) (int, error) {
	if f.countOwnedVaults == nil {
		f.unexpected("CountOwnedVaults")
	}
	return f.countOwnedVaults(ctx, tenantID, vaultIDs)
}

func (f *fakeStorage) FindMCPCredentials(ctx context.Context, query MCPCredentialQuery) ([]MCPCredentialMatch, error) {
	if f.findMCPCredentials == nil {
		f.unexpected("FindMCPCredentials")
	}
	return f.findMCPCredentials(ctx, query)
}

func (f *fakeStorage) StaticTokenCiphertext(ctx context.Context, query StaticTokenQuery) ([]byte, error) {
	if f.staticTokenCiphertxt == nil {
		f.unexpected("StaticTokenCiphertext")
	}
	return f.staticTokenCiphertxt(ctx, query)
}

// fakeOAuthTx fails the test on any call whose func the test did not set.
type fakeOAuthTx struct {
	t           testing.TB
	load        func(context.Context) (Credential, []byte, error)
	refresh     func(context.Context, SealedOAuth) error
	replacement func(context.Context, SealedOAuth) (Credential, error)
}

func (f *fakeOAuthTx) LoadOAuthCredential(ctx context.Context) (Credential, []byte, error) {
	if f.load == nil {
		f.t.Fatal("unexpected call to LoadOAuthCredential")
	}
	return f.load(ctx)
}

func (f *fakeOAuthTx) ApplyOAuthRefresh(ctx context.Context, sealed SealedOAuth) error {
	if f.refresh == nil {
		f.t.Fatal("unexpected call to ApplyOAuthRefresh")
	}
	return f.refresh(ctx, sealed)
}

func (f *fakeOAuthTx) ApplyOAuthReplacement(ctx context.Context, sealed SealedOAuth) (Credential, error) {
	if f.replacement == nil {
		f.t.Fatal("unexpected call to ApplyOAuthReplacement")
	}
	return f.replacement(ctx, sealed)
}

type refreshFunc func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error)

func (f refreshFunc) Refresh(ctx context.Context, request oauthrefresh.Request) (oauthrefresh.Token, error) {
	return f(ctx, request)
}

func unexpectedRefresh(t testing.TB) refreshFunc {
	return func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		t.Fatal("unexpected call to Refresh")
		return oauthrefresh.Token{}, nil
	}
}

func testCipher(t testing.TB) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func testService(t testing.TB, storage *fakeStorage, cipher *credentialcrypto.Cipher, refresher oauthrefresh.Refresher) *Service {
	t.Helper()
	storage.t = t
	service, err := NewService(storage, cipher, refresher)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewServiceRequiresStorageAndRefresher(t *testing.T) {
	if _, err := NewService(nil, nil, unexpectedRefresh(t)); err == nil {
		t.Fatal("missing storage accepted")
	}
	if _, err := NewService(&fakeStorage{t: t}, nil, nil); err == nil {
		t.Fatal("missing refresher accepted")
	}
	if _, err := NewService(&fakeStorage{t: t}, nil, unexpectedRefresh(t)); err != nil {
		t.Fatal("a keyless service was rejected", err)
	}
}

func TestCreateVaultValidatesBeforeStorage(t *testing.T) {
	service := testService(t, &fakeStorage{}, nil, unexpectedRefresh(t))
	long := strings.Repeat("x", 257)
	for _, command := range []CreateVault{
		{TenantID: uuid.NewString(), Name: &long},
		{TenantID: uuid.NewString(), Metadata: map[string]string{"large": strings.Repeat("x", 64*1024)}},
	} {
		if _, err := service.CreateVault(t.Context(), command); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid Vault reached storage", err)
		}
	}
	tenant, name := uuid.NewString(), "named"
	stored := Vault{ID: uuid.NewString(), TenantID: tenant}
	service = testService(t, &fakeStorage{createVault: func(_ context.Context, vault NewVault) (Vault, error) {
		if vault.TenantID != tenant || vault.Name == nil || *vault.Name != name || string(vault.Metadata) != "{}" {
			t.Fatalf("unexpected new Vault %+v", vault)
		}
		return stored, nil
	}}, nil, unexpectedRefresh(t))
	if got, err := service.CreateVault(t.Context(), CreateVault{TenantID: tenant, Name: &name}); err != nil || !reflect.DeepEqual(got, stored) {
		t.Fatal("Vault creation did not return the stored Vault", err)
	}
}

// Credential creation checks the body, then the credential key, then the
// Vault ID, and never reaches storage when one fails.
func TestCredentialCreationValidationOrder(t *testing.T) {
	tenant, url := uuid.NewString(), "https://mcp.example/tools"
	create := map[string]func(*Service, string, string) error{
		"static": func(s *Service, name, vault string) error {
			_, err := s.CreateStaticCredential(t.Context(), CreateStaticCredential{TenantID: tenant, VaultID: vault, Name: name, MCPServerURL: url, Token: "token"})
			return err
		},
		"oauth": func(s *Service, name, vault string) error {
			_, err := s.CreateOAuthCredential(t.Context(), CreateOAuthCredential{TenantID: tenant, VaultID: vault, Name: name, MCPServerURL: url, AccessToken: "token"})
			return err
		},
	}
	for kind, run := range create {
		for _, tc := range []struct {
			name   string
			cipher *credentialcrypto.Cipher
			want   error
		}{
			{"", nil, ErrInvalidInput},
			{"valid", nil, credentialcrypto.ErrUnavailable},
			{"valid", testCipher(t), ErrNotFound},
		} {
			service := testService(t, &fakeStorage{}, tc.cipher, unexpectedRefresh(t))
			if err := run(service, tc.name, "not-a-vault"); !errors.Is(err, tc.want) {
				t.Fatalf("%s creation: got %v, want %v", kind, err, tc.want)
			}
		}
	}
}

func TestCreateStaticCredentialSealsToCanonicalIDs(t *testing.T) {
	tenant, vault, url := uuid.New(), uuid.New(), "https://mcp.example/tools"
	cipher := testCipher(t)
	var stored NewCredential
	service := testService(t, &fakeStorage{createCredential: func(_ context.Context, credential NewCredential) (Credential, error) {
		stored = credential
		return Credential{ID: credential.CredentialID}, nil
	}}, cipher, unexpectedRefresh(t))
	created, err := service.CreateStaticCredential(t.Context(), CreateStaticCredential{TenantID: strings.ToUpper(tenant.String()), VaultID: strings.ToUpper(vault.String()), Name: "static", MCPServerURL: url, Token: "private-token"})
	if err != nil || created.ID != stored.CredentialID {
		t.Fatal(err)
	}
	if stored.TenantID != tenant.String() || stored.VaultID != vault.String() || stored.AuthType != AuthStaticBearer || stored.OAuthMetadata != nil || bytes.Contains(stored.Ciphertext, []byte("private-token")) {
		t.Fatalf("unexpected stored Credential %+v", stored)
	}
	plaintext, err := cipher.Open(stored.Ciphertext, credentialcrypto.Binding{TenantID: tenant.String(), VaultID: vault.String(), CredentialID: stored.CredentialID, AuthType: AuthStaticBearer, Destination: url})
	if err != nil || string(plaintext) != "private-token" {
		t.Fatal("token was not sealed to its canonical scope", err)
	}
}

func TestUpdateStaticCredential(t *testing.T) {
	tenant, vault, id, url := uuid.NewString(), uuid.NewString(), uuid.NewString(), "https://mcp.example/tools"
	command := UpdateStaticCredential{TenantID: tenant, VaultID: vault, CredentialID: id, Token: "replacement"}
	for _, malformed := range []UpdateStaticCredential{{TenantID: "x", VaultID: vault, CredentialID: id}, {TenantID: tenant, VaultID: "x", CredentialID: id}, {TenantID: tenant, VaultID: vault, CredentialID: "x"}} {
		if _, err := testService(t, &fakeStorage{}, testCipher(t), unexpectedRefresh(t)).UpdateStaticCredential(t.Context(), malformed); !errors.Is(err, ErrNotFound) {
			t.Fatal("a malformed ID named a Credential", err)
		}
	}
	current := func(authType string) func(context.Context, string, string, string) (Credential, error) {
		return func(context.Context, string, string, string) (Credential, error) {
			return Credential{ID: id, VaultID: vault, AuthType: authType, MCPServerURL: url}, nil
		}
	}
	if _, err := testService(t, &fakeStorage{getCredential: current(AuthMCPOAuth)}, testCipher(t), unexpectedRefresh(t)).UpdateStaticCredential(t.Context(), command); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("an OAuth Credential took a static token", err)
	}
	if _, err := testService(t, &fakeStorage{getCredential: current(AuthStaticBearer)}, nil, unexpectedRefresh(t)).UpdateStaticCredential(t.Context(), command); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("a keyless service replaced a token", err)
	}
	cipher := testCipher(t)
	service := testService(t, &fakeStorage{getCredential: current(AuthStaticBearer), replaceStaticToken: func(_ context.Context, replacement StaticTokenReplacement) (Credential, error) {
		plaintext, err := cipher.Open(replacement.Ciphertext, credentialcrypto.Binding{TenantID: tenant, VaultID: vault, CredentialID: id, AuthType: AuthStaticBearer, Destination: url})
		if replacement.CredentialKey != (CredentialKey{tenant, vault, id}) || replacement.MCPServerURL != url || err != nil || string(plaintext) != "replacement" {
			t.Fatalf("unexpected replacement %+v: %v", replacement, err)
		}
		return Credential{ID: id}, nil
	}}, cipher, unexpectedRefresh(t))
	if _, err := service.UpdateStaticCredential(t.Context(), command); err != nil {
		t.Fatal(err)
	}
}

func TestResolveMCPCredentials(t *testing.T) {
	tenant, vault, id, url := uuid.NewString(), uuid.NewString(), uuid.NewString(), "https://mcp.example/tools"
	command := ResolveMCPCredentials{TenantID: tenant, VaultIDs: []string{vault, vault}, Requests: []MCPCredentialRequest{
		{ServerLabel: "tools", ServerURL: url}, {ServerLabel: "anonymous", ServerURL: "https://anonymous.example/mcp"},
	}}
	owned := func(count int) func(context.Context, string, []string) (int, error) {
		return func(_ context.Context, gotTenant string, ids []string) (int, error) {
			if gotTenant != tenant || !reflect.DeepEqual(ids, []string{vault}) {
				t.Fatalf("unexpected ownership check %s %v", gotTenant, ids)
			}
			return count, nil
		}
	}
	if _, err := testService(t, &fakeStorage{countOwnedVaults: owned(0)}, nil, unexpectedRefresh(t)).ResolveMCPCredentials(t.Context(), command); !errors.Is(err, ErrNotFound) {
		t.Fatal("an unowned attached Vault was accepted", err)
	}
	service := testService(t, &fakeStorage{countOwnedVaults: owned(1), findMCPCredentials: func(_ context.Context, query MCPCredentialQuery) ([]MCPCredentialMatch, error) {
		if query.TenantID != tenant || !reflect.DeepEqual(query.VaultIDs, []string{vault}) || query.CredentialID != "" {
			t.Fatalf("unexpected lookup %+v", query)
		}
		if query.ServerURL != url {
			return nil, nil
		}
		return []MCPCredentialMatch{{VaultID: vault, CredentialID: id, AuthType: AuthStaticBearer, MCPServerURL: url}}, nil
	}}, nil, unexpectedRefresh(t))
	bindings, err := service.ResolveMCPCredentials(t.Context(), command)
	want := []MCPCredentialBinding{{ServerLabel: "tools", ServerURL: url, VaultID: vault, CredentialID: id, AuthType: AuthStaticBearer}, {ServerLabel: "anonymous", ServerURL: "https://anonymous.example/mcp"}}
	if err != nil || !reflect.DeepEqual(bindings, want) {
		t.Fatal("selection or the frozen anonymous decision differs", bindings, err)
	}
}

func TestStaticBearerToken(t *testing.T) {
	tenant, vault, id, url := uuid.NewString(), uuid.NewString(), uuid.NewString(), "https://mcp.example/tools"
	cipher := testCipher(t)
	sealed, err := cipher.Seal([]byte("private-token"), credentialcrypto.Binding{TenantID: tenant, VaultID: vault, CredentialID: id, AuthType: AuthStaticBearer, Destination: url})
	if err != nil {
		t.Fatal(err)
	}
	command := MCPBearerToken{TenantID: tenant, VaultIDs: []string{vault}, Binding: MCPCredentialBinding{ServerLabel: "tools", ServerURL: url, VaultID: vault, CredentialID: id, AuthType: AuthStaticBearer}}
	lookup := func(ciphertext []byte) func(context.Context, StaticTokenQuery) ([]byte, error) {
		return func(_ context.Context, query StaticTokenQuery) ([]byte, error) {
			if !reflect.DeepEqual(query, StaticTokenQuery{TenantID: tenant, VaultIDs: []string{vault}, VaultID: vault, CredentialID: id, MCPServerURL: url}) {
				t.Fatalf("unexpected scope %+v", query)
			}
			return ciphertext, nil
		}
	}
	if token, err := testService(t, &fakeStorage{staticTokenCiphertxt: lookup(sealed)}, cipher, unexpectedRefresh(t)).MCPBearerToken(t.Context(), command); err != nil || token != "private-token" {
		t.Fatal("static token was not opened", err)
	}
	if token, err := testService(t, &fakeStorage{staticTokenCiphertxt: lookup(sealed)}, nil, unexpectedRefresh(t)).MCPBearerToken(t.Context(), command); !errors.Is(err, credentialcrypto.ErrUnavailable) || token != "" {
		t.Fatal("a keyless service opened a token", err)
	}
	other := command
	other.Binding.ServerURL += "/other"
	if token, err := testService(t, &fakeStorage{staticTokenCiphertxt: func(context.Context, StaticTokenQuery) ([]byte, error) { return sealed, nil }}, cipher, unexpectedRefresh(t)).MCPBearerToken(t.Context(), other); err == nil || token != "" || strings.Contains(err.Error(), "private-token") {
		t.Fatal("a token opened outside its sealed destination", err)
	}
}

// oauthScenario is one mcp_oauth Credential sealed by service, served by a
// fake transaction.
type oauthScenario struct {
	service    *Service
	credential Credential
	command    MCPBearerToken
	ciphertext []byte
	refreshes  []SealedOAuth
	committed  error
}

func newOAuthScenario(t *testing.T, expiresAt time.Time, refresher oauthrefresh.Refresher) *oauthScenario {
	t.Helper()
	tenant, url := uuid.NewString(), "https://mcp.example/tools"
	expiry := expiresAt.UTC().Format(time.RFC3339Nano)
	metadata := OAuthMetadata{ExpiresAt: &expiry, Refresh: &OAuthRefreshMetadata{ClientID: "client", TokenEndpoint: "https://issuer.example/token", TokenEndpointAuth: "client_secret_basic"}}
	scenario := &oauthScenario{credential: Credential{ID: uuid.NewString(), VaultID: uuid.NewString(), Name: "OAuth", AuthType: AuthMCPOAuth, MCPServerURL: url, OAuth: &metadata}}
	storage := &fakeStorage{withOAuthCredential: func(ctx context.Context, key CredentialKey, apply func(OAuthTx) error) error {
		if key != (CredentialKey{tenant, scenario.credential.VaultID, scenario.credential.ID}) {
			t.Fatalf("unexpected key %+v", key)
		}
		err := apply(&fakeOAuthTx{t: t,
			load: func(context.Context) (Credential, []byte, error) {
				return scenario.credential, scenario.ciphertext, nil
			},
			refresh: func(_ context.Context, sealed SealedOAuth) error {
				scenario.refreshes = append(scenario.refreshes, sealed)
				return nil
			},
		})
		if err != nil {
			return err
		}
		return scenario.committed
	}}
	scenario.service = testService(t, storage, testCipher(t), refresher)
	sealed, err := scenario.service.sealOAuth(tenant, scenario.credential, oauthSecret{Version: 1, Metadata: metadata, AccessToken: "stored-access", RefreshToken: "stored-refresh", ClientSecret: "private-client"})
	if err != nil {
		t.Fatal(err)
	}
	scenario.ciphertext = sealed.Ciphertext
	scenario.command = MCPBearerToken{TenantID: tenant, VaultIDs: []string{scenario.credential.VaultID}, Binding: MCPCredentialBinding{ServerLabel: "tools", ServerURL: url,
		VaultID: scenario.credential.VaultID, CredentialID: scenario.credential.ID, AuthType: AuthMCPOAuth}}
	return scenario
}

func TestOAuthBearerTokenRefreshesExpiredGrantUnderLock(t *testing.T) {
	later := time.Now().Add(time.Hour)
	var requests []oauthrefresh.Request
	scenario := newOAuthScenario(t, time.Now().Add(-time.Minute), refreshFunc(func(_ context.Context, request oauthrefresh.Request) (oauthrefresh.Token, error) {
		requests = append(requests, request)
		return oauthrefresh.Token{AccessToken: "renewed-access", ExpiresAt: &later}, nil
	}))
	token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command)
	if err != nil || token != "renewed-access" || len(requests) != 1 || requests[0].RefreshToken != "stored-refresh" || requests[0].ClientSecret != "private-client" || len(scenario.refreshes) != 1 {
		t.Fatal("expired grant was not refreshed once", token, err)
	}
	sealed := scenario.refreshes[0]
	var metadata OAuthMetadata
	if sealed.MCPServerURL != scenario.credential.MCPServerURL || json.Unmarshal(sealed.Metadata, &metadata) != nil || *metadata.ExpiresAt != later.UTC().Format(time.RFC3339Nano) {
		t.Fatal("refreshed metadata was not stored with the grant", string(sealed.Metadata))
	}
	scenario.credential.OAuth, scenario.ciphertext = &metadata, sealed.Ciphertext
	if token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command); err != nil || token != "renewed-access" || len(requests) != 1 {
		t.Fatal("the refreshed grant was not used", token, err)
	}
}

func TestOAuthBearerTokenFailuresReturnNoToken(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	t.Run("fresh grant", func(t *testing.T) {
		scenario := newOAuthScenario(t, time.Now().Add(time.Hour), unexpectedRefresh(t))
		if token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command); err != nil || token != "stored-access" {
			t.Fatal("a fresh grant was not returned as stored", err)
		}
	})
	t.Run("unattached vault", func(t *testing.T) {
		scenario := newOAuthScenario(t, past, unexpectedRefresh(t))
		scenario.command.VaultIDs = []string{uuid.NewString()}
		scenario.service.storage = &fakeStorage{t: t}
		if token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command); !errors.Is(err, ErrNotFound) || token != "" {
			t.Fatal("an unattached Vault's grant was opened", err)
		}
	})
	t.Run("changed destination", func(t *testing.T) {
		scenario := newOAuthScenario(t, past, unexpectedRefresh(t))
		scenario.command.Binding.ServerURL += "/other"
		if token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command); !errors.Is(err, ErrNotFound) || token != "" {
			t.Fatal("a grant was used for another destination", err)
		}
	})
	t.Run("substituted metadata", func(t *testing.T) {
		scenario := newOAuthScenario(t, past, unexpectedRefresh(t))
		scenario.credential.OAuth = &OAuthMetadata{ExpiresAt: scenario.credential.OAuth.ExpiresAt, Refresh: &OAuthRefreshMetadata{ClientID: "client", TokenEndpoint: "https://attacker.example/token", TokenEndpointAuth: "client_secret_basic"}}
		if token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command); err == nil || token != "" {
			t.Fatal("substituted stored metadata was authenticated", err)
		}
	})
	t.Run("provider error", func(t *testing.T) {
		scenario := newOAuthScenario(t, past, refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
			return oauthrefresh.Token{}, errors.New("stored-refresh: provider body")
		}))
		if token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command); err == nil || token != "" || strings.Contains(err.Error(), "stored-refresh") || len(scenario.refreshes) != 0 {
			t.Fatal("a failed refresh was unsafe or stored", err)
		}
	})
	t.Run("commit failure", func(t *testing.T) {
		scenario := newOAuthScenario(t, past, refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
			return oauthrefresh.Token{AccessToken: "uncommitted-access"}, nil
		}))
		scenario.committed = errors.New("private database text")
		token, err := scenario.service.MCPBearerToken(t.Context(), scenario.command)
		if err == nil || err.Error() != "OAuth credential refresh commit failed" || token != "" {
			t.Fatal("an uncommitted grant was returned", token, err)
		}
	})
}

func TestUpdateOAuthCredentialReplacesLockedGrant(t *testing.T) {
	scenario := newOAuthScenario(t, time.Now().Add(-time.Minute), unexpectedRefresh(t))
	storage := scenario.service.storage.(*fakeStorage)
	storage.getCredential = func(context.Context, string, string, string) (Credential, error) { return scenario.credential, nil }
	lock := storage.withOAuthCredential
	var replaced SealedOAuth
	storage.withOAuthCredential = func(ctx context.Context, key CredentialKey, apply func(OAuthTx) error) error {
		return lock(ctx, key, func(tx OAuthTx) error {
			return apply(&fakeOAuthTx{t: t, load: tx.LoadOAuthCredential, replacement: func(_ context.Context, sealed SealedOAuth) (Credential, error) {
				replaced = sealed
				return scenario.credential, nil
			}})
		})
	}
	command := UpdateOAuthCredential{TenantID: scenario.command.TenantID, VaultID: scenario.credential.VaultID, CredentialID: scenario.credential.ID, AccessToken: ptr("manual-access")}
	if _, err := scenario.service.UpdateOAuthCredential(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	secret, err := scenario.service.openOAuth(command.TenantID, Credential{ID: scenario.credential.ID, VaultID: scenario.credential.VaultID, MCPServerURL: scenario.credential.MCPServerURL, OAuth: &OAuthMetadata{Refresh: scenario.credential.OAuth.Refresh}}, replaced.Ciphertext)
	if err != nil || secret.AccessToken != "manual-access" || secret.RefreshToken != "stored-refresh" || secret.Metadata.ExpiresAt != nil || !strings.Contains(string(replaced.Metadata), `"expires_at":null`) {
		t.Fatal("replacement was not sealed with its patched metadata", err)
	}
	command.ExpiresAtSet, command.ExpiresAt = true, ptr("not-a-date")
	replaced = SealedOAuth{}
	if _, err := scenario.service.UpdateOAuthCredential(t.Context(), command); !errors.Is(err, ErrInvalidInput) || replaced.Ciphertext != nil {
		t.Fatal("an invalid expiry was stored", err)
	}
}
