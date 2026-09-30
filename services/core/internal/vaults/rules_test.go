package vaults

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
)

func TestNamesAndIDs(t *testing.T) {
	for name, valid := range map[string]bool{
		"x": true, strings.Repeat("é", 128): true, strings.Repeat("x", 256): true,
		"": false, strings.Repeat("x", 257): false, strings.Repeat("é", 129): false, string([]byte{0xff}): false,
	} {
		if validName(name) != valid {
			t.Fatalf("validName(%d bytes) = %t", len(name), !valid)
		}
	}
	id := uuid.New()
	for raw, want := range map[string]string{
		id.String(): id.String(), strings.ToUpper(id.String()): id.String(), "{" + id.String() + "}": id.String(),
		"": "", "invalid": "", uuid.Nil.String(): "",
	} {
		got, ok := canonicalID(raw)
		if got != want || ok != (want != "") {
			t.Fatalf("canonicalID(%q) = %q, %t", raw, got, ok)
		}
	}
}

func TestPageQueryValidate(t *testing.T) {
	statuses, err := PageQuery{Limit: 1}.Validate()
	if err != nil || !reflect.DeepEqual(statuses, []string{StatusActive, StatusArchived}) {
		t.Fatal("empty statuses do not select every status", statuses, err)
	}
	statuses, err = PageQuery{Limit: 100, Statuses: []string{StatusArchived}}.Validate()
	if err != nil || !reflect.DeepEqual(statuses, []string{StatusArchived}) {
		t.Fatal("status filter changed", statuses, err)
	}
	for _, query := range []PageQuery{{Limit: 0}, {Limit: 101}, {Limit: 20, Statuses: []string{"deleted"}}} {
		if _, err := query.Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid query %+v accepted: %v", query, err)
		}
	}
}

func TestMCPCredentialSelectionRules(t *testing.T) {
	first, second := uuid.NewString(), uuid.NewString()
	attached, err := attachedVaultIDs([]string{first, strings.ToUpper(first), second})
	if err != nil || !reflect.DeepEqual(attached, []string{first, second}) {
		t.Fatal("attached Vaults were not canonical and deduplicated", attached, err)
	}
	if _, err := attachedVaultIDs([]string{first, "invalid"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("a malformed attached Vault named one", err)
	}
	url := "https://mcp.example/tools"
	named := func(id string) MCPCredentialRequest {
		return MCPCredentialRequest{ServerLabel: "tools", ServerURL: url, CredentialID: &id}
	}
	for _, tc := range []struct {
		request  MCPCredentialRequest
		attached []string
		want     string
		err      error
	}{
		{MCPCredentialRequest{ServerLabel: "tools", ServerURL: url}, nil, "", nil},
		{named(strings.ToUpper(second)), attached, second, nil},
		{MCPCredentialRequest{ServerURL: url}, attached, "", ErrInvalidInput},
		{MCPCredentialRequest{ServerLabel: "tools"}, attached, "", ErrInvalidInput},
		{named(second), nil, "", mcpCredentialRequiresVault()},
		{named("not-a-credential"), attached, "", mcpCredentialNotAttached("not-a-credential")},
	} {
		got, err := mcpCredentialLookup(tc.request, tc.attached)
		if got != tc.want || !sameError(err, tc.err) {
			t.Fatalf("lookup %+v = %q, %v", tc.request, got, err)
		}
	}
	match := MCPCredentialMatch{VaultID: first, CredentialID: second, AuthType: AuthStaticBearer, MCPServerURL: url}
	bound := MCPCredentialBinding{ServerLabel: "tools", ServerURL: url, VaultID: first, CredentialID: second, AuthType: AuthStaticBearer}
	for _, tc := range []struct {
		request MCPCredentialRequest
		matches []MCPCredentialMatch
		want    MCPCredentialBinding
		err     error
	}{
		{MCPCredentialRequest{ServerLabel: "tools", ServerURL: url}, nil, MCPCredentialBinding{ServerLabel: "tools", ServerURL: url}, nil},
		{MCPCredentialRequest{ServerLabel: "tools", ServerURL: url}, []MCPCredentialMatch{match}, bound, nil},
		{named(second), []MCPCredentialMatch{match}, bound, nil},
		{MCPCredentialRequest{ServerLabel: "tools", ServerURL: url}, []MCPCredentialMatch{match, match}, MCPCredentialBinding{}, mcpCredentialAmbiguous(url)},
		{named(second), nil, MCPCredentialBinding{}, mcpCredentialNotAttached(second)},
		{MCPCredentialRequest{ServerLabel: "tools", ServerURL: url + "/other", CredentialID: &second}, []MCPCredentialMatch{match}, MCPCredentialBinding{}, mcpCredentialURLMismatch(second, url+"/other")},
	} {
		got, err := selectMCPCredential(tc.request, tc.matches)
		if got != tc.want || !sameError(err, tc.err) {
			t.Fatalf("select %+v = %+v, %v", tc.request, got, err)
		}
	}
}

func TestMCPCredentialSelectionMessages(t *testing.T) {
	id, url := uuid.NewString(), "https://mcp.example/tools"
	for _, tc := range []struct {
		err      error
		conflict bool
		message  string
	}{
		{mcpCredentialRequiresVault(), false, "MCP credential_id requires an attached vault"},
		{mcpCredentialNotAttached(id), false, "MCP credential_id " + id + " was not found in an attached vault"},
		{mcpCredentialURLMismatch(id, url), false, "MCP credential_id " + id + " does not match server_url " + url},
		{mcpCredentialAmbiguous(url), true, "multiple attached vault credentials match MCP server_url " + url + "; specify credential_id"},
		// A value beyond the shared echo bound is left out.
		{mcpCredentialNotAttached(strings.Repeat("x", 4096)), false, "MCP credential_id was not found in an attached vault"},
	} {
		var selection *MCPCredentialSelectionError
		if !errors.As(tc.err, &selection) || selection.Conflict != tc.conflict || selection.Message != tc.message {
			t.Fatalf("selection error %v, want %q", tc.err, tc.message)
		}
	}
}

func TestBearerTokenScope(t *testing.T) {
	tenant, vault, credential := uuid.New(), uuid.New(), uuid.New()
	binding := MCPCredentialBinding{ServerLabel: "tools", ServerURL: "https://mcp.example/tools", VaultID: strings.ToUpper(vault.String()), CredentialID: credential.String(), AuthType: AuthStaticBearer}
	scope, err := bearerTokenScope(MCPBearerToken{TenantID: tenant.String(), VaultIDs: []string{vault.String()}, Binding: binding})
	want := bearerScope{tenantID: tenant.String(), vaultID: vault.String(), credentialID: credential.String(), attached: []string{vault.String()}}
	if err != nil || !reflect.DeepEqual(scope, want) || !scope.vaultAttached() {
		t.Fatal("frozen scope was not canonical", scope, err)
	}
	for _, mutate := range []func(*MCPBearerToken){
		func(c *MCPBearerToken) { c.TenantID = "invalid" },
		func(c *MCPBearerToken) { c.VaultIDs = []string{"invalid"} },
		func(c *MCPBearerToken) { c.Binding.VaultID = "" },
		func(c *MCPBearerToken) { c.Binding.CredentialID = uuid.Nil.String() },
		func(c *MCPBearerToken) { c.Binding.AuthType = "other" },
		func(c *MCPBearerToken) { c.Binding.ServerURL = "" },
	} {
		command := MCPBearerToken{TenantID: tenant.String(), VaultIDs: []string{vault.String()}, Binding: binding}
		mutate(&command)
		if _, err := bearerTokenScope(command); !errors.Is(err, ErrNotFound) {
			t.Fatal("an incomplete frozen binding was admitted", command, err)
		}
	}
	scope, err = bearerTokenScope(MCPBearerToken{TenantID: tenant.String(), Binding: binding})
	if err != nil || scope.vaultAttached() {
		t.Fatal("an unattached Vault was reported attached", err)
	}
}

func TestOAuthCreationRules(t *testing.T) {
	refresh := func(method string) *OAuthRefreshMetadata {
		return &OAuthRefreshMetadata{ClientID: "client", TokenEndpoint: "https://issuer.example/token", TokenEndpointAuth: method}
	}
	valid := CreateOAuthCredential{Name: "OAuth", MCPServerURL: "https://mcp.example/tools"}
	for _, tc := range []struct {
		mutate func(*CreateOAuthCredential)
		valid  bool
	}{
		{func(*CreateOAuthCredential) {}, true},
		{func(c *CreateOAuthCredential) { c.OAuth.ExpiresAt = ptr("2030-01-02T03:04:05.123456789Z") }, true},
		{func(c *CreateOAuthCredential) {
			c.OAuth.Refresh, c.RefreshToken, c.ClientSecret = refresh("client_secret_post"), "r", "s"
		}, true},
		{func(c *CreateOAuthCredential) { c.OAuth.Refresh, c.RefreshToken = refresh("none"), "r" }, true},
		{func(c *CreateOAuthCredential) { c.Name = "" }, false},
		{func(c *CreateOAuthCredential) { c.MCPServerURL = "" }, false},
		{func(c *CreateOAuthCredential) { c.OAuth.ExpiresAt = ptr("not-a-date") }, false},
		{func(c *CreateOAuthCredential) { c.OAuth.Refresh = refresh("private_key_jwt") }, false},
		{func(c *CreateOAuthCredential) { c.OAuth.Refresh = &OAuthRefreshMetadata{TokenEndpointAuth: "none"} }, false},
		{func(c *CreateOAuthCredential) { c.RefreshToken = "r" }, false},
		{func(c *CreateOAuthCredential) { c.ClientSecret = "s" }, false},
		{func(c *CreateOAuthCredential) { c.OAuth.Refresh, c.ClientSecret = refresh("none"), "s" }, false},
	} {
		command := valid
		tc.mutate(&command)
		if validOAuthCreation(command) != tc.valid {
			t.Fatalf("validOAuthCreation(%+v) = %t", command, !tc.valid)
		}
	}
}

func TestOAuthUpdateRules(t *testing.T) {
	expiry := "2030-01-02T03:04:05Z"
	stored := OAuthGrant{AccessToken: "access", RefreshToken: "refresh", ClientSecret: "secret",
		Metadata: OAuthMetadata{ExpiresAt: &expiry, Refresh: &OAuthRefreshMetadata{ClientID: "client", TokenEndpoint: "https://issuer.example/token", TokenEndpointAuth: "client_secret_basic", Scope: ptr("read write")}}}
	got, err := applyOAuthUpdate(stored, UpdateOAuthCredential{})
	if err != nil || !reflect.DeepEqual(got, stored) {
		t.Fatal("an empty patch changed the grant", err)
	}
	got, err = applyOAuthUpdate(stored, UpdateOAuthCredential{AccessToken: ptr("new")})
	if err != nil || got.AccessToken != "new" || got.Metadata.ExpiresAt != nil || got.RefreshToken != "refresh" || got.ClientSecret != "secret" {
		t.Fatal("a new access token kept the old expiry or changed other secrets", err)
	}
	got, err = applyOAuthUpdate(stored, UpdateOAuthCredential{AccessToken: ptr("new"), ExpiresAtSet: true, ExpiresAt: ptr("2031-01-01T00:00:00Z")})
	if err != nil || *got.Metadata.ExpiresAt != "2031-01-01T00:00:00Z" {
		t.Fatal("an explicit expiry was not kept", err)
	}
	got, err = applyOAuthUpdate(stored, UpdateOAuthCredential{Refresh: &OAuthRefreshUpdate{RefreshToken: ptr("r2"), ClientSecret: ptr("s2"), ScopeSet: true, TokenEndpointAuthType: "client_secret_basic"}})
	if err != nil || got.RefreshToken != "r2" || got.ClientSecret != "s2" || got.Metadata.Refresh.Scope != nil || *stored.Metadata.Refresh.Scope != "read write" {
		t.Fatal("refresh patch was not applied, or it changed the stored copy", err)
	}
	none := stored
	none.Metadata.Refresh = &OAuthRefreshMetadata{ClientID: "client", TokenEndpoint: "https://issuer.example/token", TokenEndpointAuth: "none"}
	withoutRefresh := stored
	withoutRefresh.Metadata.Refresh = nil
	for _, tc := range []struct {
		secret OAuthGrant
		patch  OAuthRefreshUpdate
	}{
		{withoutRefresh, OAuthRefreshUpdate{RefreshToken: ptr("cannot-add")}},
		{stored, OAuthRefreshUpdate{TokenEndpointAuthType: "client_secret_post"}},
		{none, OAuthRefreshUpdate{ClientSecret: ptr("unused")}},
	} {
		if _, err := applyOAuthUpdate(tc.secret, UpdateOAuthCredential{Refresh: &tc.patch}); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("an invalid refresh patch was applied", tc.patch, err)
		}
	}
}

func TestOAuthAccessTokenAndRefreshRules(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(when time.Time) *string { value := when.Format(time.RFC3339Nano); return &value }
	for _, tc := range []struct {
		expiresAt *string
		access    string
		token     string
		expired   bool
		failed    bool
	}{
		{nil, "access", "access", false, false},
		{at(now.Add(time.Second)), "access", "access", false, false},
		{at(now), "access", "", true, false},
		{at(now.Add(-time.Hour)), "", "", true, false},
		{nil, "", "", false, true},
		{ptr("not-a-date"), "access", "", false, true},
	} {
		token, expired, err := currentAccessToken(OAuthGrant{AccessToken: tc.access, Metadata: OAuthMetadata{ExpiresAt: tc.expiresAt}}, now)
		if token != tc.token || expired != tc.expired || (err != nil) != tc.failed {
			t.Fatalf("currentAccessToken(%v, %q) = %q, %t, %v", tc.expiresAt, tc.access, token, expired, err)
		}
	}
	secret := OAuthGrant{RefreshToken: "refresh", ClientSecret: "secret", Metadata: OAuthMetadata{Refresh: &OAuthRefreshMetadata{
		ClientID: "client", TokenEndpoint: "https://issuer.example/token", TokenEndpointAuth: "client_secret_post", Resource: ptr("https://mcp.example/tools"), Scope: ptr("read")}}}
	request, err := refreshRequest(secret)
	want := oauthrefresh.Request{TokenEndpoint: "https://issuer.example/token", ClientID: "client", AuthMethod: "client_secret_post", ClientSecret: "secret", RefreshToken: "refresh", Resource: ptr("https://mcp.example/tools"), Scope: ptr("read")}
	if err != nil || !reflect.DeepEqual(request, want) {
		t.Fatal("refresh request lost grant fields", err)
	}
	for _, unusable := range []OAuthGrant{{RefreshToken: "refresh"}, {Metadata: secret.Metadata}} {
		if _, err := refreshRequest(unusable); err == nil {
			t.Fatal("a grant without refresh configuration or token was refreshed")
		}
	}
	later := now.Add(time.Hour)
	refreshed, err := applyRefreshedToken(secret, oauthrefresh.Token{AccessToken: "renewed", ExpiresAt: &later}, now)
	if err != nil || refreshed.AccessToken != "renewed" || refreshed.RefreshToken != "refresh" || *refreshed.Metadata.ExpiresAt != later.Format(time.RFC3339Nano) {
		t.Fatal("refreshed grant lost the stored refresh token or the expiry", err)
	}
	refreshed, err = applyRefreshedToken(refreshed, oauthrefresh.Token{AccessToken: "rotated", RefreshToken: "next"}, now)
	if err != nil || refreshed.RefreshToken != "next" || refreshed.Metadata.ExpiresAt != nil {
		t.Fatal("rotated refresh token or unknown expiry was not stored", err)
	}
	for _, token := range []oauthrefresh.Token{{}, {AccessToken: "stale", ExpiresAt: &now}} {
		if _, err := applyRefreshedToken(secret, token, now); err == nil {
			t.Fatal("an unusable refresh result was stored")
		}
	}
}

func sameError(got, want error) bool {
	var selection *MCPCredentialSelectionError
	if errors.As(want, &selection) {
		var actual *MCPCredentialSelectionError
		return errors.As(got, &actual) && *actual == *selection
	}
	return errors.Is(got, want)
}

func ptr(value string) *string { return &value }
