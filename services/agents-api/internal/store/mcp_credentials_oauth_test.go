package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/oauthrefresh"
	"github.com/google/uuid"
)

func TestOAuthRefreshPersistsRotatedGrantAndRequest(t *testing.T) {
	for _, method := range []string{"none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			var request oauthrefresh.Request
			expiry := time.Now().Add(time.Hour).UTC()
			s, pool, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(_ context.Context, r oauthrefresh.Request) (oauthrefresh.Token, error) {
				request = r
				return oauthrefresh.Token{AccessToken: "renewed-access", RefreshToken: "rotated-refresh", ExpiresAt: &expiry}, nil
			}))
			input.OAuth.Refresh.TokenEndpointAuth = method
			if method == "none" {
				input.ClientSecret = ""
			}
			credential := createOAuthFixture(t, s, tenant, vault, input)
			binding := oauthFixtureBinding(credential)
			got, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, binding)
			if err != nil || got != "renewed-access" {
				t.Fatal("refresh did not return committed access", err)
			}
			want := oauthrefresh.Request{TokenEndpoint: input.OAuth.Refresh.TokenEndpoint, ClientID: input.OAuth.Refresh.ClientID, AuthMethod: method, ClientSecret: input.ClientSecret, RefreshToken: input.RefreshToken, Resource: input.OAuth.Refresh.Resource, Scope: input.OAuth.Refresh.Scope}
			if !reflect.DeepEqual(request, want) {
				t.Fatal("refresh request lost grant fields")
			}
			restarted := NewWithCredentialCipherAndOAuthRefresh(pool, s.credentialCipher, nil)
			after := storedOAuthSecret(t, restarted, tenant, credential)
			if after.AccessToken != "renewed-access" || after.RefreshToken != "rotated-refresh" || after.Metadata.ExpiresAt == nil || *after.Metadata.ExpiresAt != expiry.Format(time.RFC3339Nano) {
				t.Fatal("refreshed grant not durable")
			}
			got, err = restarted.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, binding)
			if err != nil || got != "renewed-access" {
				t.Fatal("fresh grant needed another refresh after restart", err)
			}
			metadata, err := New(pool).GetCredential(t.Context(), tenant, vault.ID, credential.ID)
			if err != nil || !reflect.DeepEqual(metadata.OAuth, &after.Metadata) {
				t.Fatal("safe expiry metadata did not follow refresh", err)
			}
		})
	}
}

func TestOAuthRefreshPreservesRefreshTokenWhenOmitted(t *testing.T) {
	s, _, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		return oauthrefresh.Token{AccessToken: "renewed-access"}, nil
	}))
	credential := createOAuthFixture(t, s, tenant, vault, input)
	if _, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential)); err != nil {
		t.Fatal(err)
	}
	secret := storedOAuthSecret(t, s, tenant, credential)
	if secret.RefreshToken != input.RefreshToken || secret.Metadata.ExpiresAt != nil {
		t.Fatal("omitted refresh token or unknown expiry changed incorrectly")
	}
}

func TestOAuthFreshAndUnknownExpiryDoNotRefresh(t *testing.T) {
	var requests atomic.Int32
	s, _, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		requests.Add(1)
		return oauthrefresh.Token{}, errors.New("unexpected refresh")
	}))
	for _, expiry := range []*string{nil, oauthString(time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))} {
		input.OAuth.ExpiresAt = expiry
		credential := createOAuthFixture(t, s, tenant, vault, input)
		if token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential)); err != nil || token != input.AccessToken {
			t.Fatal("usable token was not returned", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("non-expired grant refreshed")
	}
	input.AccessToken = ""
	input.OAuth.ExpiresAt = nil
	credential := createOAuthFixture(t, s, tenant, vault, input)
	if token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential)); err == nil || token != "" {
		t.Fatal("empty access token admitted")
	}
}

func TestOAuthConcurrentRefreshUsesOneCommittedGrant(t *testing.T) {
	var requests atomic.Int32
	expiry := time.Now().Add(time.Hour)
	s, pool, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		requests.Add(1)
		return oauthrefresh.Token{AccessToken: "concurrent-access", RefreshToken: "single-use-next", ExpiresAt: &expiry}, nil
	}))
	credential := createOAuthFixture(t, s, tenant, vault, input)
	var workers sync.WaitGroup
	errorsFound := make(chan error, 12)
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			// Separate Store instances exercise PostgreSQL serialization, not a local lock.
			reader := NewWithCredentialCipherAndOAuthRefresh(pool, s.credentialCipher, s.oauthRefresher)
			token, err := reader.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential))
			if err == nil && token != "concurrent-access" {
				err = errors.New("concurrent lookup returned stale token")
			}
			errorsFound <- err
		}()
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
			s, pool, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(ctx context.Context, _ oauthrefresh.Request) (oauthrefresh.Token, error) {
				close(entered)
				select {
				case <-release:
					return oauthrefresh.Token{AccessToken: "refreshed-access", RefreshToken: "rotated-refresh", ExpiresAt: &expiry}, nil
				case <-ctx.Done():
					return oauthrefresh.Token{}, ctx.Err()
				}
			}))
			credential := createOAuthFixture(t, s, tenant, vault, input)
			refreshed := make(chan error, 1)
			go func() {
				_, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential))
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
					_, err = s.UpdateOAuthCredential(t.Context(), tenant, vault.ID, credential.ID, UpdateOAuthCredentialInput{AccessToken: oauthString("manual-access"), Refresh: &OAuthRefreshUpdate{RefreshToken: oauthString("manual-refresh")}})
				case "credential-delete":
					_, err = New(pool).DeleteCredential(t.Context(), tenant, vault.ID, credential.ID)
				case "vault-delete":
					_, err = New(pool).DeleteVault(t.Context(), tenant, vault.ID)
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
				secret := storedOAuthSecret(t, s, tenant, credential)
				if secret.AccessToken != "manual-access" || secret.RefreshToken != "manual-refresh" || secret.Metadata.ExpiresAt != nil {
					t.Fatal("late refresh overwrote manual replacement")
				}
			} else {
				if _, err := s.GetCredential(t.Context(), tenant, vault.ID, credential.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("deleted credential resurrected")
				}
				if token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential)); !errors.Is(err, ErrNotFound) || token != "" {
					t.Fatal("deleted grant remained usable")
				}
			}
		})
	}
}

func TestOAuthCancelledRefreshRollsBackAndAllowsReplacement(t *testing.T) {
	entered := make(chan struct{})
	s, _, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(ctx context.Context, _ oauthrefresh.Request) (oauthrefresh.Token, error) {
		close(entered)
		<-ctx.Done()
		return oauthrefresh.Token{}, ctx.Err()
	}))
	credential := createOAuthFixture(t, s, tenant, vault, input)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.MCPBearerToken(ctx, tenant, []string{vault.ID}, oauthFixtureBinding(credential))
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
	if _, err := s.UpdateOAuthCredential(t.Context(), tenant, vault.ID, credential.ID, UpdateOAuthCredentialInput{AccessToken: oauthString("manual-after-cancel")}); err != nil {
		t.Fatal("cancelled refresh retained ownership", err)
	}
	if token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential)); err != nil || token != "manual-after-cancel" {
		t.Fatal("replacement after cancellation unusable")
	}
}

func TestOAuthDeletionCannotReselectAnotherCredential(t *testing.T) {
	s, _, tenant, vault, input := oauthFixture(t, nil)
	input.OAuth.ExpiresAt = nil
	credential := createOAuthFixture(t, s, tenant, vault, input)
	bindings, err := s.ResolveMCPCredentials(t.Context(), tenant, []string{vault.ID}, []MCPCredentialRequest{{ServerLabel: "test", ServerURL: input.MCPServerURL}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteCredential(t.Context(), tenant, vault.ID, credential.ID); err != nil {
		t.Fatal(err)
	}
	input.AccessToken = uuid.NewString()
	createOAuthFixture(t, s, tenant, vault, input)
	if token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, bindings[0]); !errors.Is(err, ErrNotFound) || token != "" {
		t.Fatal("frozen identity fell back after deletion")
	}
}

func TestOAuthRefreshCommitFailureDoesNotReturnUncommittedToken(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	s, pool, tenant, vault, input := oauthFixture(t, oauthRefreshFunc(func(context.Context, oauthrefresh.Request) (oauthrefresh.Token, error) {
		return oauthrefresh.Token{AccessToken: "uncommitted-access", RefreshToken: "uncommitted-refresh", ExpiresAt: &expiry}, nil
	}))
	credential := createOAuthFixture(t, s, tenant, vault, input)
	before := storedOAuthSecret(t, s, tenant, credential)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	function, trigger := "oauth_commit_fail_"+suffix, "oauth_commit_fail_"+suffix
	// A deferred trigger exercises failure after the UPDATE has returned metadata.
	// The condition confines the synthetic failure to this test's unique grant.
	if _, err := pool.Exec(t.Context(), "CREATE FUNCTION "+function+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-refresh-canary'; END $$"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), "DROP FUNCTION "+function+"() CASCADE")
		if err != nil {
			t.Error(err)
		}
	})
	if _, err := pool.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER "+trigger+" AFTER UPDATE ON vault_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.id='"+credential.ID+"'::uuid) EXECUTE FUNCTION "+function+"()"); err != nil {
		t.Fatal(err)
	}
	token, err := s.MCPBearerToken(t.Context(), tenant, []string{vault.ID}, oauthFixtureBinding(credential))
	if err == nil || token != "" || strings.Contains(err.Error(), "private-refresh-canary") {
		t.Fatal("commit failure returned an uncommitted grant or unsafe error")
	}
	if after := storedOAuthSecret(t, s, tenant, credential); !reflect.DeepEqual(before, after) {
		t.Fatal("failed commit modified the grant")
	}
}
