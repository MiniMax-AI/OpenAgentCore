package vaultpg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/vaultpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// oauthFixture is one tenant's Vault served with a fixed credential key. Its
// command creates an expired grant with refresh configuration.
type oauthFixture struct {
	store   *vaultpg.Store
	pool    *pgxpool.Pool
	cipher  *credentialcrypto.Cipher
	service *vaults.Service
	tenant  string
	vault   vaults.Vault
	command vaults.CreateOAuthCredential
}

// newOAuthFixture fails the test on any exchange when refresher is nil.
func newOAuthFixture(t *testing.T, refresher oauthrefresh.Refresher) *oauthFixture {
	t.Helper()
	store, pool := openStore(t)
	cipher := newCipher(t, bytes.Repeat([]byte{17}, 32))
	service := newService(t, store, cipher, refresher)
	tenant := uuid.NewString()
	vault := createVault(t, service, tenant)
	return &oauthFixture{store: store, pool: pool, cipher: cipher, service: service, tenant: tenant, vault: vault,
		command: vaults.CreateOAuthCredential{TenantID: tenant, VaultID: vault.ID, Name: "OAuth fixture", MCPServerURL: "https://mcp.example/tools",
			AccessToken: "private-access-canary", RefreshToken: "private-refresh-canary", ClientSecret: "private-client-canary",
			OAuth: vaults.OAuthMetadata{ExpiresAt: ptr(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)),
				Refresh: &vaults.OAuthRefreshMetadata{ClientID: "test-client", TokenEndpoint: "https://issuer.example/token",
					TokenEndpointAuth: "client_secret_basic", Resource: ptr("https://mcp.example/tools"), Scope: ptr("read write")}}}}
}

// otherService serves the fixture's database through a separate Store, so
// competing callers meet only in PostgreSQL.
func (f *oauthFixture) otherService(t *testing.T, cipher *credentialcrypto.Cipher, refresher oauthrefresh.Refresher) *vaults.Service {
	t.Helper()
	return newService(t, vaultpg.New(pgunit.NewPool(f.pool)), cipher, refresher)
}

func (f *oauthFixture) create(t *testing.T, command vaults.CreateOAuthCredential) vaults.Credential {
	t.Helper()
	credential, err := f.service.CreateOAuthCredential(t.Context(), command)
	if err != nil {
		t.Fatal("create OAuth fixture", err)
	}
	return credential
}

func (f *oauthFixture) token(ctx context.Context, service *vaults.Service, credential vaults.Credential) (string, error) {
	return bearerToken(ctx, service, f.tenant, []string{f.vault.ID}, oauthBinding(credential))
}

func (f *oauthFixture) update(ctx context.Context, service *vaults.Service, credential vaults.Credential, patch vaults.UpdateOAuthCredential) (vaults.Credential, error) {
	patch.TenantID, patch.VaultID, patch.CredentialID = f.tenant, credential.VaultID, credential.ID
	return service.UpdateOAuthCredential(ctx, patch)
}

func (f *oauthFixture) grant(t *testing.T, credential vaults.Credential) storedGrant {
	t.Helper()
	return readGrant(t, f.pool, f.cipher, f.tenant, credential)
}

func oauthBinding(credential vaults.Credential) vaults.MCPCredentialBinding {
	return vaults.MCPCredentialBinding{ServerLabel: "test", ServerURL: credential.MCPServerURL,
		VaultID: credential.VaultID, CredentialID: credential.ID, AuthType: credential.AuthType}
}

func ptr[T any](value T) *T { return &value }

func TestOAuthCredentialMetadataEncryptionAndScope(t *testing.T) {
	var exchanges atomic.Int32
	counting := refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		exchanges.Add(1)
		return oauthrefresh.Token{}, errors.New("unexpected exchange")
	})
	f := newOAuthFixture(t, counting)
	input := f.command
	credential := f.create(t, input)
	got, err := f.store.GetCredential(t.Context(), f.tenant, f.vault.ID, credential.ID)
	if err != nil || !reflect.DeepEqual(got, credential) {
		t.Fatal("keyless safe metadata changed", err)
	}
	page, err := f.store.ListCredentials(t.Context(), f.tenant, f.vault.ID, vaults.PageQuery{Limit: 20, Ascending: true})
	if err != nil || len(page.Credentials) != 1 || !reflect.DeepEqual(page.Credentials[0], credential) {
		t.Fatal("keyless listing failed", err)
	}
	encoded, _ := json.Marshal(page)
	var ciphertext []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT token_ciphertext FROM vault_credentials WHERE id=$1", credential.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{input.AccessToken, input.RefreshToken, input.ClientSecret} {
		if bytes.Contains(encoded, []byte(secret)) || bytes.Contains(ciphertext, []byte(secret)) {
			t.Fatal("metadata disclosed a secret or plaintext grant persisted")
		}
	}
	if grant := readGrant(t, f.pool, newCipher(t, bytes.Repeat([]byte{17}, 32)), f.tenant, credential); grant.AccessToken != input.AccessToken || grant.RefreshToken != input.RefreshToken || grant.ClientSecret != input.ClientSecret {
		t.Fatal("restart lost grant material")
	}
	binding := oauthBinding(credential)
	for _, target := range []struct {
		tenant  string
		vaults  []string
		binding vaults.MCPCredentialBinding
	}{
		{uuid.NewString(), []string{f.vault.ID}, binding}, {f.tenant, nil, binding},
		{f.tenant, []string{f.vault.ID}, vaults.MCPCredentialBinding{ServerLabel: "test", ServerURL: input.MCPServerURL + "/other", VaultID: f.vault.ID, CredentialID: credential.ID, AuthType: vaults.AuthMCPOAuth}},
	} {
		if token, err := bearerToken(t.Context(), f.service, target.tenant, target.vaults, target.binding); !errors.Is(err, vaults.ErrNotFound) || token != "" {
			t.Fatal("foreign or mismatched scope admitted", err)
		}
	}
	if _, err := f.service.UpdateOAuthCredential(t.Context(), vaults.UpdateOAuthCredential{TenantID: uuid.NewString(), VaultID: f.vault.ID, CredentialID: credential.ID, AccessToken: ptr("replacement")}); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("foreign update admitted")
	}
	foreign := input
	foreign.TenantID = uuid.NewString()
	if _, err := f.service.CreateOAuthCredential(t.Context(), foreign); !errors.Is(err, vaults.ErrNotFound) {
		t.Fatal("foreign creation admitted")
	}
	keyless := f.otherService(t, nil, counting)
	if _, err := keyless.CreateOAuthCredential(t.Context(), input); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("keyless creation admitted")
	}
	if token, err := f.token(t.Context(), keyless, credential); !errors.Is(err, credentialcrypto.ErrUnavailable) || token != "" {
		t.Fatal("keyless execution admitted")
	}
	wrong := f.otherService(t, newCipher(t, bytes.Repeat([]byte{18}, 32)), counting)
	if token, err := f.token(t.Context(), wrong, credential); err == nil || token != "" {
		t.Fatal("wrong key executed")
	}
	if exchanges.Load() != 0 {
		t.Fatal("resource operations or invalid scopes contacted provider")
	}
}

func TestOAuthCredentialUpdatesPreservePinnedSemantics(t *testing.T) {
	f := newOAuthFixture(t, nil)
	input := f.command
	credential := f.create(t, input)
	update := func(patch vaults.UpdateOAuthCredential) vaults.Credential {
		t.Helper()
		got, err := f.update(t.Context(), f.service, credential, patch)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != credential.ID || got.Name != credential.Name || got.AuthType != credential.AuthType || got.MCPServerURL != credential.MCPServerURL || !got.CreatedAt.Equal(credential.CreatedAt) {
			t.Fatal("immutable metadata changed")
		}
		return got
	}
	unchanged := update(vaults.UpdateOAuthCredential{})
	if !reflect.DeepEqual(unchanged.OAuth, credential.OAuth) {
		t.Fatal("omitted values changed")
	}
	replacement := update(vaults.UpdateOAuthCredential{AccessToken: ptr("new-access")})
	if replacement.OAuth.ExpiresAt != nil {
		t.Fatal("new access token retained old expiry")
	}
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	replacement = update(vaults.UpdateOAuthCredential{ExpiresAtSet: true, ExpiresAt: &expiry, Refresh: &vaults.OAuthRefreshUpdate{ScopeSet: true, Scope: nil}})
	if replacement.OAuth.ExpiresAt == nil || *replacement.OAuth.ExpiresAt != expiry || replacement.OAuth.Refresh.Scope != nil {
		t.Fatal("expiry or null scope semantics failed")
	}
	grant := f.grant(t, replacement)
	if grant.AccessToken != "new-access" || grant.RefreshToken != input.RefreshToken || grant.ClientSecret != input.ClientSecret {
		t.Fatal("omitted secrets were replaced")
	}
	replacement = update(vaults.UpdateOAuthCredential{ExpiresAtSet: true, Refresh: &vaults.OAuthRefreshUpdate{RefreshToken: ptr("new-refresh"), TokenEndpointAuthType: "client_secret_basic", ClientSecret: ptr("new-secret"), ScopeSet: true, Scope: ptr("read")}})
	grant = f.grant(t, replacement)
	if grant.Metadata.ExpiresAt != nil || grant.RefreshToken != "new-refresh" || grant.ClientSecret != "new-secret" || *grant.Metadata.Refresh.Scope != "read" {
		t.Fatal("replacement fields not persisted")
	}
	for _, patch := range []vaults.UpdateOAuthCredential{
		{ExpiresAtSet: true, ExpiresAt: ptr("not-a-date")},
		{Refresh: &vaults.OAuthRefreshUpdate{TokenEndpointAuthType: "client_secret_post"}},
	} {
		if _, err := f.update(t.Context(), f.service, credential, patch); !errors.Is(err, vaults.ErrInvalidInput) {
			t.Fatal("invalid mutation admitted")
		}
	}
	if got := f.grant(t, replacement); !reflect.DeepEqual(got, grant) {
		t.Fatal("rejected update changed the grant")
	}
	input.OAuth.Refresh = nil
	input.RefreshToken = ""
	input.ClientSecret = ""
	withoutRefresh := f.create(t, input)
	if _, err := f.update(t.Context(), f.service, withoutRefresh, vaults.UpdateOAuthCredential{Refresh: &vaults.OAuthRefreshUpdate{RefreshToken: ptr("cannot-add")}}); !errors.Is(err, vaults.ErrInvalidInput) {
		t.Fatal("added missing refresh configuration")
	}
}

func TestOAuthMetadataTamperingNeverReachesProvider(t *testing.T) {
	var exchanges atomic.Int32
	f := newOAuthFixture(t, refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		exchanges.Add(1)
		return oauthrefresh.Token{}, errors.New("unexpected refresh")
	}))
	for _, mutation := range []string{
		`jsonb_set(oauth_metadata,'{refresh,token_endpoint}','"https://attacker.example/token"')`,
		`jsonb_set(oauth_metadata,'{refresh,client_id}','"other-client"')`,
		`jsonb_set(oauth_metadata,'{refresh,scope}','"all"')`,
		`jsonb_set(oauth_metadata,'{expires_at}','null')`,
	} {
		credential := f.create(t, f.command)
		if _, err := f.pool.Exec(t.Context(), "UPDATE vault_credentials SET oauth_metadata="+mutation+" WHERE id=$1", credential.ID); err != nil {
			t.Fatal(err)
		}
		if token, err := f.token(t.Context(), f.service, credential); err == nil || token != "" {
			t.Fatal("metadata substitution executed")
		}
		if _, err := f.update(t.Context(), f.service, credential, vaults.UpdateOAuthCredential{AccessToken: ptr("new")}); err == nil {
			t.Fatal("update authenticated substituted metadata")
		}
	}
	if exchanges.Load() != 0 {
		t.Fatal("tampered metadata reached token endpoint")
	}
}

func TestOAuthCredentialSelectionIncludesBothAuthTypes(t *testing.T) {
	f := newOAuthFixture(t, nil)
	credential := f.create(t, f.command)
	requests := []vaults.MCPCredentialRequest{{ServerLabel: "test", ServerURL: f.command.MCPServerURL}}
	bindings := resolve(t, f.service, f.tenant, []string{f.vault.ID}, requests...)
	if bindings[0].AuthType != vaults.AuthMCPOAuth || bindings[0].CredentialID != credential.ID {
		t.Fatal("OAuth was not selected")
	}
	createStatic(t, f.service, f.tenant, f.vault.ID, "Static", f.command.MCPServerURL, "static")
	_, err := f.service.ResolveMCPCredentials(t.Context(), vaults.ResolveMCPCredentials{TenantID: f.tenant, VaultIDs: []string{f.vault.ID}, Requests: requests})
	if !isSelectionError(err, true, "multiple attached vault credentials match MCP server_url "+f.command.MCPServerURL+"; specify credential_id") {
		t.Fatal("ambiguous mixed credentials selected", err)
	}
	requests[0].CredentialID = &credential.ID
	if selected := resolve(t, f.service, f.tenant, []string{f.vault.ID}, requests...); selected[0] != bindings[0] {
		t.Fatal("explicit OAuth identity changed")
	}
}

func TestOAuthRefreshErrorsAreSafeAndPreserveGrant(t *testing.T) {
	for _, mode := range []string{"provider_error", "empty_access", "expired_response", "no_refresh"} {
		t.Run(mode, func(t *testing.T) {
			f := newOAuthFixture(t, refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
				switch mode {
				case "provider_error":
					return oauthrefresh.Token{}, errors.New("private-refresh-canary: provider body")
				case "expired_response":
					past := time.Now().Add(-time.Second)
					return oauthrefresh.Token{AccessToken: "new", ExpiresAt: &past}, nil
				}
				return oauthrefresh.Token{}, nil
			}))
			input := f.command
			if mode == "no_refresh" {
				input.OAuth.Refresh = nil
				input.RefreshToken = ""
				input.ClientSecret = ""
			}
			credential := f.create(t, input)
			before := f.grant(t, credential)
			token, err := f.token(t.Context(), f.service, credential)
			if err == nil || token != "" || strings.Contains(err.Error(), "private-refresh-canary") {
				t.Fatal("refresh failed unsafely")
			}
			if after := f.grant(t, credential); !reflect.DeepEqual(before, after) {
				t.Fatal("failed refresh changed grant")
			}
		})
	}
}

func TestOAuthRefreshPersistsRotatedGrantAndRequest(t *testing.T) {
	for _, method := range []string{"none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			var request oauthrefresh.Request
			expiry := time.Now().Add(time.Hour).UTC()
			f := newOAuthFixture(t, refreshFunc(func(_ context.Context, r oauthrefresh.Request) (oauthrefresh.Token, error) {
				request = r
				return oauthrefresh.Token{AccessToken: "renewed-access", RefreshToken: "rotated-refresh", ExpiresAt: &expiry}, nil
			}))
			input := f.command
			input.OAuth.Refresh.TokenEndpointAuth = method
			if method == "none" {
				input.ClientSecret = ""
			}
			credential := f.create(t, input)
			got, err := f.token(t.Context(), f.service, credential)
			if err != nil || got != "renewed-access" {
				t.Fatal("refresh did not return committed access", err)
			}
			want := oauthrefresh.Request{TokenEndpoint: input.OAuth.Refresh.TokenEndpoint, ClientID: input.OAuth.Refresh.ClientID, AuthMethod: method, ClientSecret: input.ClientSecret, RefreshToken: input.RefreshToken, Resource: input.OAuth.Refresh.Resource, Scope: input.OAuth.Refresh.Scope}
			if !reflect.DeepEqual(request, want) {
				t.Fatal("refresh request lost grant fields")
			}
			restartedStore, restartedPool := openStore(t)
			after := readGrant(t, restartedPool, f.cipher, f.tenant, credential)
			if after.AccessToken != "renewed-access" || after.RefreshToken != "rotated-refresh" || after.Metadata.ExpiresAt == nil || *after.Metadata.ExpiresAt != expiry.Format(time.RFC3339Nano) {
				t.Fatal("refreshed grant not durable")
			}
			restarted := newService(t, restartedStore, f.cipher, nil)
			got, err = f.token(t.Context(), restarted, credential)
			if err != nil || got != "renewed-access" {
				t.Fatal("fresh grant needed another refresh after restart", err)
			}
			metadata, err := restartedStore.GetCredential(t.Context(), f.tenant, f.vault.ID, credential.ID)
			if err != nil || !reflect.DeepEqual(metadata.OAuth, &after.Metadata) {
				t.Fatal("safe expiry metadata did not follow refresh", err)
			}
		})
	}
}

func TestOAuthRefreshPreservesRefreshTokenWhenOmitted(t *testing.T) {
	f := newOAuthFixture(t, refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		return oauthrefresh.Token{AccessToken: "renewed-access"}, nil
	}))
	credential := f.create(t, f.command)
	if _, err := f.token(t.Context(), f.service, credential); err != nil {
		t.Fatal(err)
	}
	if grant := f.grant(t, credential); grant.RefreshToken != f.command.RefreshToken || grant.Metadata.ExpiresAt != nil {
		t.Fatal("omitted refresh token or unknown expiry changed incorrectly")
	}
}

func TestOAuthFreshAndUnknownExpiryDoNotRefresh(t *testing.T) {
	f := newOAuthFixture(t, nil)
	input := f.command
	for _, expiry := range []*string{nil, ptr(time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))} {
		input.OAuth.ExpiresAt = expiry
		credential := f.create(t, input)
		if token, err := f.token(t.Context(), f.service, credential); err != nil || token != input.AccessToken {
			t.Fatal("usable token was not returned", err)
		}
	}
	input.AccessToken = ""
	input.OAuth.ExpiresAt = nil
	credential := f.create(t, input)
	if token, err := f.token(t.Context(), f.service, credential); err == nil || token != "" {
		t.Fatal("empty access token admitted")
	}
}

func TestOAuthConcurrentRefreshUsesOneCommittedGrant(t *testing.T) {
	var requests atomic.Int32
	expiry := time.Now().Add(time.Hour)
	refresher := refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		requests.Add(1)
		return oauthrefresh.Token{AccessToken: "concurrent-access", RefreshToken: "single-use-next", ExpiresAt: &expiry}, nil
	})
	f := newOAuthFixture(t, refresher)
	credential := f.create(t, f.command)
	// Separate Stores and services exercise PostgreSQL serialization, not a
	// local lock.
	var callers []*vaults.Service
	for range 12 {
		callers = append(callers, f.otherService(t, f.cipher, refresher))
	}
	var workers sync.WaitGroup
	errorsFound := make(chan error, len(callers))
	start := make(chan struct{})
	for _, caller := range callers {
		workers.Go(func() {
			<-start
			token, err := f.token(t.Context(), caller, credential)
			if err == nil && token != "concurrent-access" {
				err = errors.New("concurrent lookup returned stale token")
			}
			errorsFound <- err
		})
	}
	close(start)
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("one expiring grant triggered duplicate provider exchanges")
	}
}

func TestOAuthRefreshSerializesReplacementAndDeletion(t *testing.T) {
	for _, mutation := range []string{"replacement", "credential-delete", "vault-delete"} {
		t.Run(mutation, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			expiry := time.Now().Add(time.Hour)
			f := newOAuthFixture(t, refreshFunc(func(ctx context.Context, _ oauthrefresh.Request) (oauthrefresh.Token, error) {
				close(entered)
				select {
				case <-release:
					return oauthrefresh.Token{AccessToken: "refreshed-access", RefreshToken: "rotated-refresh", ExpiresAt: &expiry}, nil
				case <-ctx.Done():
					return oauthrefresh.Token{}, ctx.Err()
				}
			}))
			credential := f.create(t, f.command)
			other := f.otherService(t, f.cipher, nil)
			refreshed := make(chan error, 1)
			go func() {
				_, err := f.token(t.Context(), f.service, credential)
				refreshed <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("refresh never reached provider")
			}
			mutated := make(chan error, 1)
			go func() {
				var err error
				switch mutation {
				case "replacement":
					_, err = f.update(t.Context(), other, credential, vaults.UpdateOAuthCredential{AccessToken: ptr("manual-access"), Refresh: &vaults.OAuthRefreshUpdate{RefreshToken: ptr("manual-refresh")}})
				case "credential-delete":
					_, err = other.DeleteCredential(t.Context(), vaults.DeleteCredential{TenantID: f.tenant, VaultID: f.vault.ID, CredentialID: credential.ID})
				case "vault-delete":
					_, err = other.DeleteVault(t.Context(), vaults.DeleteVault{TenantID: f.tenant, VaultID: f.vault.ID})
				}
				mutated <- err
			}()
			select {
			case <-mutated:
				t.Fatal("mutation bypassed pending refresh ownership")
			case <-time.After(75 * time.Millisecond):
			}
			unblock()
			for _, completed := range []chan error{refreshed, mutated} {
				select {
				case err := <-completed:
					if err != nil {
						t.Fatal("serialized operation failed", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("serialized operation deadlocked")
				}
			}
			if mutation == "replacement" {
				grant := f.grant(t, credential)
				if grant.AccessToken != "manual-access" || grant.RefreshToken != "manual-refresh" || grant.Metadata.ExpiresAt != nil {
					t.Fatal("late refresh overwrote manual replacement")
				}
				return
			}
			if _, err := f.store.GetCredential(t.Context(), f.tenant, f.vault.ID, credential.ID); !errors.Is(err, vaults.ErrNotFound) {
				t.Fatal("deleted credential resurrected")
			}
			if token, err := f.token(t.Context(), f.service, credential); !errors.Is(err, vaults.ErrNotFound) || token != "" {
				t.Fatal("deleted grant remained usable")
			}
		})
	}
}

func TestOAuthCancelledRefreshRollsBackAndAllowsReplacement(t *testing.T) {
	entered := make(chan struct{})
	f := newOAuthFixture(t, refreshFunc(func(ctx context.Context, _ oauthrefresh.Request) (oauthrefresh.Token, error) {
		close(entered)
		<-ctx.Done()
		return oauthrefresh.Token{}, ctx.Err()
	}))
	credential := f.create(t, f.command)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.token(ctx, f.service, credential)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh not started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled refresh succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not release refresh")
	}
	if _, err := f.update(t.Context(), f.service, credential, vaults.UpdateOAuthCredential{AccessToken: ptr("manual-after-cancel")}); err != nil {
		t.Fatal("cancelled refresh retained ownership", err)
	}
	if token, err := f.token(t.Context(), f.service, credential); err != nil || token != "manual-after-cancel" {
		t.Fatal("replacement after cancellation unusable")
	}
}

func TestOAuthDeletionCannotReselectAnotherCredential(t *testing.T) {
	f := newOAuthFixture(t, nil)
	input := f.command
	input.OAuth.ExpiresAt = nil
	credential := f.create(t, input)
	bindings := resolve(t, f.service, f.tenant, []string{f.vault.ID}, vaults.MCPCredentialRequest{ServerLabel: "test", ServerURL: input.MCPServerURL})
	if _, err := f.service.DeleteCredential(t.Context(), vaults.DeleteCredential{TenantID: f.tenant, VaultID: f.vault.ID, CredentialID: credential.ID}); err != nil {
		t.Fatal(err)
	}
	input.AccessToken = uuid.NewString()
	f.create(t, input)
	if token, err := bearerToken(t.Context(), f.service, f.tenant, []string{f.vault.ID}, bindings[0]); !errors.Is(err, vaults.ErrNotFound) || token != "" {
		t.Fatal("frozen identity fell back after deletion")
	}
}

func TestOAuthRefreshCommitFailureDoesNotReturnUncommittedToken(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	f := newOAuthFixture(t, refreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		return oauthrefresh.Token{AccessToken: "uncommitted-access", RefreshToken: "uncommitted-refresh", ExpiresAt: &expiry}, nil
	}))
	credential := f.create(t, f.command)
	before := f.grant(t, credential)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	function, trigger := "oauth_commit_fail_"+suffix, "oauth_commit_fail_"+suffix
	// A deferred trigger fails the commit after the UPDATE has returned
	// metadata. The condition confines the failure to this test's grant.
	if _, err := f.pool.Exec(t.Context(), "CREATE FUNCTION "+function+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-refresh-canary'; END $$"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), "DROP FUNCTION "+function+"() CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.pool.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER "+trigger+" AFTER UPDATE ON vault_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.id='"+credential.ID+"'::uuid) EXECUTE FUNCTION "+function+"()"); err != nil {
		t.Fatal(err)
	}
	token, err := f.token(t.Context(), f.service, credential)
	if err == nil || token != "" || err.Error() != "OAuth credential refresh commit failed" {
		t.Fatal("commit failure returned an uncommitted grant or unsafe error", err)
	}
	if after := f.grant(t, credential); !reflect.DeepEqual(before, after) {
		t.Fatal("failed commit modified the grant")
	}
}
